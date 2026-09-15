package runner

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/metrics"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/samplelog"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	maxRampDuration = 5 * time.Second // 档位启动错峰窗口的上限

	// preflightTimeout 预检单请求的超时。预检要完整走完一次流式生成，
	// 思考型模型的思考阶段动辄超过 30s，超时过短会把慢模型误判为不连通，
	// 进而中止整个压测。
	preflightTimeout = 2 * time.Minute
)

// RunBenchmark 遍历 models × concurrency 组合，顺序运行并返回所有聚合结果。
// ctx 取消时提前结束，返回已完成部分的结果。
func RunBenchmark(ctx context.Context, cfg types.BenchmarkConfig, rep reporter.Reporter) ([]types.AggregatedMetrics, error) {
	ConfigureClient(slices.Max(cfg.Concurrency))

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
			rate := levelTargetRate(cfg, i)
			rep.LevelStart(seq, total, model, conc, time.Now().Add(cfg.Duration))

			result := RunLevel(ctx, cfg, model, conc, rate, RampDuration(conc, cfg.Duration), rep)
			samplelog.WriteLevel(samplelog.LevelContext{
				Model:       model.Name,
				Provider:    model.Provider,
				TokenGroup:  model.TokenGroup,
				Concurrency: conc,
				TargetRate:  rate,
				LevelStart:  result.Start,
			}, result.Metrics)
			agg := metrics.AggregateMetrics(result, cfg.SLO, cfg.ShowHistogram)
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

// levelTargetRate 返回第 i 档的目标 RPS：closed-loop（cfg.OpenLoop 为 false）恒为 0，
// open-loop 时取 cfg.RequestRate[i]（config.validate 已保证与 Concurrency 等长）。
func levelTargetRate(cfg types.BenchmarkConfig, i int) float64 {
	if !cfg.OpenLoop || i >= len(cfg.RequestRate) {
		return 0
	}
	return cfg.RequestRate[i]
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
		m := PreflightModel(ctx, cfg, model)
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

// PreflightModel 对单个模型发送一次完整的流式预检请求，返回请求指标。
// 超时预算 preflightTimeout，覆盖思考型模型的长思考阶段。
func PreflightModel(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	reqCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	if fn, ok := doSSERequests[model.Provider]; ok {
		return fn(reqCtx, cfg, model)
	}
	return types.RequestMetrics{Success: false, Error: "unknown provider: " + string(model.Provider)}
}

// warmupModel 在 model 的正式档位开始前做短暂预热，让连接池和运行时热身，丢弃结果。
// 预热并发取首个正式档位的并发数：把首档所需的连接提前建好，
// 否则首档要独自承担全部冷启动建连开销，指标被系统性抬高。
// 预热紧贴各自模型的首档执行，而不是开测前统一预热全部模型：
// 排在后面的模型要等前面模型跑完全部档位才轮到自己，若提前预热，
// 上游侧的热身效果（模型驻留、扩容）在正式压测时早已衰减，
// 冷启动开销仍会落进该模型首档的测量窗口。
// open-loop 时预热档的目标 RPS 取首档的 RequestRate[0]，与正式档位口径一致。
func warmupModel(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, idx int, rep reporter.Reporter) {
	warmupCfg := cfg
	warmupCfg.Duration = cfg.WarmupDuration
	warmupConc := cfg.Concurrency[0]
	rate := levelTargetRate(cfg, 0)
	rep.WarmupStart(warmupConc, cfg.WarmupDuration)
	rep.WarmupModel(idx+1, len(cfg.Models), model, time.Now().Add(cfg.WarmupDuration))
	RunLevel(ctx, warmupCfg, model, warmupConc, rate, RampDuration(warmupConc, warmupCfg.Duration), rep)
	rep.WarmupEnd()
}

// RampDuration 计算档位启动的错峰窗口：按约 1ms/worker 随并发数增长，
// 上限 maxRampDuration 且不超过压测时长的 1/6。一次性启动数万协程会让
// TLS 握手全部挤在档位开头，本地排队时间计入首批请求的 TTFT/时延，
// 污染整个窗口的 P95/P99；错峰启动把建连压力摊开。
func RampDuration(concurrency int, total time.Duration) time.Duration {
	return min(time.Duration(concurrency)*time.Millisecond, maxRampDuration, total/6)
}

// ShouldStopEarly 判断当前档位的累计失败率是否超过早停阈值。
// 样本数未达 minSamples 时不评估，避免开局几条请求的抖动误判整个档位不可用。
func ShouldStopEarly(total, failed int64, minSamples int, maxErrorRate float64) bool {
	if total < int64(minSamples) {
		return false
	}
	return float64(failed)/float64(total) > maxErrorRate
}

// levelState 保存一个档位运行期间的共享状态：累计样本、早停计数器。
// closed-loop 和 open-loop 两种分发方式共用同一份记账逻辑，
// 避免早停判定、样本落地这两段逻辑写两遍、行为分叉。
type levelState struct {
	mu             sync.Mutex
	requestMetrics []types.RequestMetrics
	totalCnt       atomic.Int64
	failCnt        atomic.Int64
	stoppedEarly   atomic.Bool
}

// record 落地一条请求结果并做早停判定；levelCtx.Err() != nil 且请求失败时
// （中止导致的在途请求失败）直接丢弃，避免污染结果。
func (s *levelState) record(cfg types.BenchmarkConfig, rep reporter.Reporter, m types.RequestMetrics, levelCtx context.Context, cancelLevel context.CancelFunc) {
	if levelCtx.Err() != nil && !m.Success {
		return
	}

	s.mu.Lock()
	s.requestMetrics = append(s.requestMetrics, m)
	s.mu.Unlock()
	rep.RequestDone(m)

	if cfg.EarlyStopEnabled {
		tot := s.totalCnt.Add(1)
		f := s.failCnt.Load()
		if !m.Success {
			f = s.failCnt.Add(1)
		}
		if ShouldStopEarly(tot, f, cfg.MinSamples, cfg.MaxErrorRate) {
			s.stoppedEarly.Store(true)
			cancelLevel()
		}
	}
}

// RunLevel 以指定并发数持续发送请求，直到 deadline 或 ctx 取消为止。
// targetRate 为 0 时走 closed-loop（现有行为：每个 worker 等响应才发下一个）；
// targetRate > 0 时走 open-loop（按泊松过程到达发送请求，不等响应），
// concurrency 此时改为“同时在途请求数上限”，防止过载时本地无限堆积协程/连接。
// ramp 是首批请求的错峰启动窗口，仅 closed-loop 使用（单机路径由 RampDuration
// 按本档并发计算；分布式路径由 coordinator 按全局并发统一算好后下发，各节点
// 共用同一窗口）；open-loop 的到达时刻本身已被泊松过程随机打散，无需额外错峰。
// 若 cfg.EarlyStopEnabled 且档位内失败率超过 cfg.MaxErrorRate，会提前取消 levelCtx
// 结束本档位（不影响其他档位或外层 ctx），并在返回结果中标记 StoppedEarly。
func RunLevel(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, concurrency int, targetRate float64, ramp time.Duration, rep reporter.Reporter) types.BenchmarkResult {
	start := time.Now()
	deadline := start.Add(cfg.Duration)

	levelCtx, cancelLevel := context.WithCancel(ctx)
	defer cancelLevel()

	state := &levelState{}

	if targetRate > 0 {
		runLevelOpenLoop(levelCtx, cfg, model, concurrency, targetRate, deadline, rep, state, cancelLevel)
	} else {
		runLevelClosedLoop(levelCtx, cfg, model, concurrency, ramp, deadline, rep, state, cancelLevel)
	}

	return types.BenchmarkResult{
		Model:        model.Name,
		Provider:     model.Provider,
		TokenGroup:   model.TokenGroup,
		Concurrency:  concurrency,
		TargetRate:   targetRate,
		Start:        start,
		Window:       cfg.Duration,
		Elapsed:      time.Since(start),
		Metrics:      state.requestMetrics,
		StoppedEarly: state.stoppedEarly.Load(),
	}
}

// dispatchOne 发起一次请求并返回带时间戳的指标，未知 provider 时返回失败指标。
func dispatchOne(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	reqStart := time.Now()
	var m types.RequestMetrics
	if fn, ok := doSSERequests[model.Provider]; ok {
		m = fn(ctx, cfg, model)
	} else {
		m = types.RequestMetrics{Success: false, Error: "unknown provider: " + string(model.Provider)}
	}
	m.Timestamp = reqStart
	return m
}

// runLevelClosedLoop 是现有行为：固定 concurrency 个协程，每个协程循环
// “发请求 → 等响应 → 等 cfg.ThinkTime → 发下一个”，直到 deadline 或取消。
func runLevelClosedLoop(levelCtx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, concurrency int, ramp time.Duration, deadline time.Time, rep reporter.Reporter, state *levelState, cancelLevel context.CancelFunc) {
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
				m := dispatchOne(levelCtx, cfg, model)
				state.record(cfg, rep, m, levelCtx, cancelLevel)

				// deadline 已过就直接退出：此时的思考时间等待毫无意义，
				// 徒增 Elapsed（排空期被多算约一个 ThinkTime）
				if !time.Now().Before(deadline) {
					return
				}
				// ThinkTime 为 0（完成即发）时不进 select：拿不到定时器也少一次调度，
				// 这正是与 evalscope `--rate -1` 对拍时的口径
				if cfg.ThinkTime <= 0 {
					continue
				}
				// 思考时间截断到 deadline：睡过头只会让本档 Elapsed 白白变长
				//（排空期多算一个 ThinkTime），醒来后照样因超期退出。
				// 硬编码 300ms 时这点溢出可忽略，配置成几十秒后就不能忽略了。
				wait := min(cfg.ThinkTime, time.Until(deadline))
				if wait <= 0 {
					return
				}
				select {
				case <-levelCtx.Done():
					return
				case <-time.After(wait):
				}
			}
		})
	}
	wg.Wait()
}

// runLevelOpenLoop 按泊松过程生成到达间隔（指数分布，均值 1/targetRate），
// 到点即发一个请求（不等上一个响应），是避免 Coordinated Omission 的关键：
// closed-loop 在服务端过载时会因为“等响应才发下一个”自动降速，看起来吞吐/延迟
// 都还行，但那只是压测客户端自己让路的假象；open-loop 按目标速率持续到达，
// 才能测出真实流量下的排队延迟和尾延迟。
// concurrency 用作同时在途请求数上限（channel 信号量）：超过上限时，下一次
// 到达会阻塞等空位再发出，对应 AIPerf 里 request-rate + max-concurrency 的双控——
// 既保留“到达按目标速率”的开环语义，又防止过载时协程/连接本地无限堆积。
func runLevelOpenLoop(levelCtx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec, concurrency int, targetRate float64, deadline time.Time, rep reporter.Reporter, state *levelState, cancelLevel context.CancelFunc) {
	sem := make(chan struct{}, max(concurrency, 1))
	var wg sync.WaitGroup

	for time.Now().Before(deadline) && levelCtx.Err() == nil {
		select {
		case sem <- struct{}{}:
		case <-levelCtx.Done():
			wg.Wait()
			return
		}

		wg.Go(func() {
			defer func() { <-sem }()
			m := dispatchOne(levelCtx, cfg, model)
			state.record(cfg, rep, m, levelCtx, cancelLevel)
		})

		if !time.Now().Before(deadline) {
			break
		}

		// 逆变换采样：dt = -ln(1-U)/rate，U~Uniform(0,1)，得到均值为
		// 1/targetRate 的指数分布到达间隔（泊松过程的到达间隔即指数分布）。
		dt := time.Duration(-math.Log(1-rand.Float64()) / targetRate * float64(time.Second))
		timer := time.NewTimer(dt)
		select {
		case <-levelCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}

	wg.Wait()
}
