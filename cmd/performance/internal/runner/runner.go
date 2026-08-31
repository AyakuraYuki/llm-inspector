package runner

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/metrics"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	cooldownPerRequest = 300 * time.Millisecond // 协程内发送每个请求的间隔时间
	maxRampDuration    = 5 * time.Second        // 档位启动错峰窗口的上限

	// preflightTimeout 预检单请求的超时。预检要完整走完一次流式生成，
	// 思考型模型的思考阶段动辄超过 30s，超时过短会把慢模型误判为不连通，
	// 进而中止整个压测。
	preflightTimeout = 2 * time.Minute
)

// RunBenchmark 遍历 models × concurrency 组合，顺序运行并返回所有聚合结果。
// ctx 取消时提前结束，返回已完成部分的结果。
func RunBenchmark(ctx context.Context, cfg types.BenchmarkConfig, rep reporter.Reporter) ([]types.AggregatedMetrics, error) {
	configureSharedClient(slices.Max(cfg.Concurrency))

	if err := preflightCheck(ctx, cfg, rep); err != nil {
		return nil, err
	}

	total := len(cfg.Models) * len(cfg.Concurrency)
	var results []types.AggregatedMetrics
	seq := 0

loop:
	for mi, model := range cfg.Models {
		if cfg.Warmup && ctx.Err() == nil {
			warmupModel(ctx, cfg, model, mi, rep)
		}
		for i, conc := range cfg.Concurrency {
			if ctx.Err() != nil {
				break loop
			}
			seq++
			rep.LevelStart(seq, total, model, conc, time.Now().Add(cfg.Duration))

			result := runConcurrent(ctx, cfg, model, conc, rep)
			agg := metrics.AggregateMetrics(result)
			results = append(results, agg)
			rep.LevelEnd(agg)

			if agg.StoppedEarly {
				errRate := 0.0
				if agg.Total > 0 {
					errRate = float64(agg.Failed) / float64(agg.Total)
				}
				rep.EarlyStop(model, conc, errRate)
				if cfg.SkipHigherConcurrency {
					// 跳过的档位计入进度序号，避免 [seq/total] 卡在半路不动
					seq += len(cfg.Concurrency) - i - 1
					break // 跳出并发档位循环，进入下一个模型；不再执行 cooldown
				}
			}

			if i < len(cfg.Concurrency)-1 && cfg.CooldownDuration > 0 && ctx.Err() == nil {
				rep.CooldownStart(cfg.CooldownDuration)
				select {
				case <-ctx.Done():
				case <-time.After(cfg.CooldownDuration):
				}
			}
		}
	}

	rep.BenchmarkEnd(ctx.Err() != nil)
	return results, nil
}

// preflightCheck 对每个模型发送一次请求，任何失败均返回错误终止压测。
// 目的是在大规模压测开始前，快速验证上游渠道配置、Token 有效性和网络连通性。
func preflightCheck(ctx context.Context, cfg types.BenchmarkConfig, rep reporter.Reporter) error {
	rep.PreflightStart(len(cfg.Models))
	allOK := true
	for _, model := range cfg.Models {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reqCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
		var m types.RequestMetrics
		if fn, ok := doSSERequests[model.Provider]; ok {
			m = fn(reqCtx, cfg, model)
		} else {
			m = types.RequestMetrics{Success: false, Error: "unknown provider: " + string(model.Provider)}
		}
		cancel()
		rep.PreflightResult(model, m)
		if !m.Success {
			allOK = false
		}
	}
	rep.PreflightEnd(allOK)
	if !allOK {
		return errors.New("部分模型连通性验证失败，请检查上游渠道配置后重试")
	}
	return nil
}

// warmupModel 在 model 的正式档位开始前做短暂预热，让连接池和运行时热身，丢弃结果。
// 预热并发取首个正式档位的并发数：把首档所需的连接提前建好，
// 否则首档要独自承担全部冷启动建连开销，指标被系统性抬高。
// 预热紧贴各自模型的首档执行，而不是开测前统一预热全部模型：
// 排在后面的模型要等前面模型跑完全部档位才轮到自己，若提前预热，
// 上游侧的热身效果（模型驻留、扩容）在正式压测时早已衰减，
// 冷启动开销仍会落进该模型首档的测量窗口。
func warmupModel(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, idx int, rep reporter.Reporter) {
	warmupCfg := cfg
	warmupCfg.Duration = cfg.WarmupDuration
	warmupConc := cfg.Concurrency[0]
	rep.WarmupStart(warmupConc, cfg.WarmupDuration)
	rep.WarmupModel(idx+1, len(cfg.Models), model, time.Now().Add(cfg.WarmupDuration))
	runConcurrent(ctx, warmupCfg, model, warmupConc, rep)
	rep.WarmupEnd()
}

// rampDuration 计算档位启动的错峰窗口：按约 1ms/worker 随并发数增长，
// 上限 maxRampDuration 且不超过压测时长的 1/6。一次性启动数万协程会让
// TLS 握手全部挤在档位开头，本地排队时间计入首批请求的 TTFT/时延，
// 污染整个窗口的 P95/P99；错峰启动把建连压力摊开。
func rampDuration(concurrency int, total time.Duration) time.Duration {
	return min(time.Duration(concurrency)*time.Millisecond, maxRampDuration, total/6)
}

// shouldStopEarly 判断当前档位的累计失败率是否超过早停阈值。
// 样本数未达 minSamples 时不评估，避免开局几条请求的抖动误判整个档位不可用。
func shouldStopEarly(total, failed int64, minSamples int, maxErrorRate float64) bool {
	if total < int64(minSamples) {
		return false
	}
	return float64(failed)/float64(total) > maxErrorRate
}

// runConcurrent 以指定并发数并发发送请求，持续到 deadline 或 ctx 取消为止。
// 若 cfg.EarlyStopEnabled 且档位内失败率超过 cfg.MaxErrorRate，会提前取消 levelCtx
// 结束本档位（不影响其他档位或外层 ctx），并在返回结果中标记 StoppedEarly。
func runConcurrent(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, concurrency int, rep reporter.Reporter) types.BenchmarkResult {
	start := time.Now()
	deadline := start.Add(cfg.Duration)
	ramp := rampDuration(concurrency, cfg.Duration)

	levelCtx, cancelLevel := context.WithCancel(ctx)
	defer cancelLevel()

	var (
		mu                sync.Mutex
		requestMetrics    []types.RequestMetrics
		totalCnt, failCnt atomic.Int64
		stoppedEarly      atomic.Bool
	)

	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Go(func() {
			// 错峰启动：第 i 个 worker 延迟 ramp*i/concurrency 后发出首个请求
			if delay := ramp * time.Duration(i) / time.Duration(concurrency); delay > 0 {
				select {
				case <-levelCtx.Done():
					return
				case <-time.After(delay):
				}
			}
			for time.Now().Before(deadline) && levelCtx.Err() == nil {
				reqStart := time.Now()
				var m types.RequestMetrics

				if fn, ok := doSSERequests[model.Provider]; ok {
					m = fn(levelCtx, cfg, model)
				} else {
					m = types.RequestMetrics{
						Success: false,
						Error:   "unknown provider: " + string(model.Provider),
					}
				}
				m.Timestamp = reqStart

				// 中止导致的在途请求失败不计入结果，避免污染指标
				if levelCtx.Err() != nil && !m.Success {
					return
				}

				mu.Lock()
				requestMetrics = append(requestMetrics, m)
				mu.Unlock()
				rep.RequestDone(m)

				if cfg.EarlyStopEnabled {
					tot := totalCnt.Add(1)
					f := failCnt.Load()
					if !m.Success {
						f = failCnt.Add(1)
					}
					if shouldStopEarly(tot, f, cfg.MinSamples, cfg.MaxErrorRate) {
						stoppedEarly.Store(true)
						cancelLevel()
					}
				}

				// deadline 已过就直接退出：此时的请求间隔等待毫无意义，
				// 徒增 Elapsed（排空期被多算约一个 cooldownPerRequest）
				if !time.Now().Before(deadline) {
					return
				}
				select {
				case <-levelCtx.Done():
					return
				case <-time.After(cooldownPerRequest):
				}
			}
		})
	}

	wg.Wait()

	return types.BenchmarkResult{
		Model:        model.Name,
		Provider:     model.Provider,
		TokenGroup:   model.TokenGroup,
		Concurrency:  concurrency,
		Start:        start,
		Window:       cfg.Duration,
		Elapsed:      time.Since(start),
		Metrics:      requestMetrics,
		StoppedEarly: stoppedEarly.Load(),
	}
}
