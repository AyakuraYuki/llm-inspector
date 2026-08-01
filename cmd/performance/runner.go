package main

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

const (
	warmupConcurrency  = 10                     // 预热使用的并发数
	cooldownPerRequest = 300 * time.Millisecond // 协程内发送每个请求的间隔时间
)

// preflightCheck 对每个模型发送一次请求，任何失败均返回错误终止压测。
// 目的是在大规模压测开始前，快速验证上游渠道配置、Token 有效性和网络连通性。
func preflightCheck(ctx context.Context, cfg BenchmarkConfig, rep Reporter) error {
	rep.PreflightStart(len(cfg.Models))
	allOK := true
	for _, model := range cfg.Models {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var m RequestMetrics
		if fn, ok := doSSERequests[model.Provider]; ok {
			m = fn(reqCtx, cfg, model)
		} else {
			m = RequestMetrics{Success: false, Error: "unknown provider: " + string(model.Provider)}
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

// warmupModels 以较小并发对每个模型做短暂预热，让连接池和运行时热身，丢弃结果。
func warmupModels(ctx context.Context, cfg BenchmarkConfig, rep Reporter) {
	warmupCfg := cfg
	warmupCfg.Duration = cfg.WarmupDuration
	rep.WarmupStart(warmupConcurrency, cfg.WarmupDuration)
	for i, model := range cfg.Models {
		if ctx.Err() != nil {
			return
		}
		rep.WarmupModel(i+1, len(cfg.Models), model, time.Now().Add(cfg.WarmupDuration))
		runConcurrent(ctx, warmupCfg, model, warmupConcurrency, rep)
	}
	rep.WarmupEnd()
}

// runBenchmark 遍历 models × concurrency 组合，顺序运行并返回所有聚合结果。
// ctx 取消时提前结束，返回已完成部分的结果。
func runBenchmark(ctx context.Context, cfg BenchmarkConfig, rep Reporter) ([]AggregatedMetrics, error) {
	configureSharedClient(slices.Max(cfg.Concurrency))

	if err := preflightCheck(ctx, cfg, rep); err != nil {
		return nil, err
	}

	if cfg.Warmup && ctx.Err() == nil {
		warmupModels(ctx, cfg, rep)
	}

	total := len(cfg.Models) * len(cfg.Concurrency)
	var results []AggregatedMetrics
	seq := 0

loop:
	for _, model := range cfg.Models {
		for i, conc := range cfg.Concurrency {
			if ctx.Err() != nil {
				break loop
			}
			seq++
			rep.LevelStart(seq, total, model, conc, time.Now().Add(cfg.Duration))

			result := runConcurrent(ctx, cfg, model, conc, rep)
			agg := aggregateMetrics(result)
			results = append(results, agg)
			rep.LevelEnd(agg)

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

// runConcurrent 以指定并发数并发发送请求，持续到 deadline 或 ctx 取消为止。
func runConcurrent(ctx context.Context, cfg BenchmarkConfig, model ModelSpec, concurrency int, rep Reporter) BenchmarkResult {
	start := time.Now()
	deadline := start.Add(cfg.Duration)

	var (
		mu      sync.Mutex
		metrics []RequestMetrics
	)

	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			for time.Now().Before(deadline) && ctx.Err() == nil {
				reqStart := time.Now()
				var m RequestMetrics

				if fn, ok := doSSERequests[model.Provider]; ok {
					m = fn(ctx, cfg, model)
				} else {
					m = RequestMetrics{
						Success: false,
						Error:   "unknown provider: " + string(model.Provider),
					}
				}
				m.Timestamp = reqStart

				// 中止导致的在途请求失败不计入结果，避免污染指标
				if ctx.Err() != nil && !m.Success {
					return
				}

				mu.Lock()
				metrics = append(metrics, m)
				mu.Unlock()
				rep.RequestDone(m)

				time.Sleep(cooldownPerRequest)
			}
		})
	}

	wg.Wait()

	return BenchmarkResult{
		Model:       model.Name,
		Provider:    model.Provider,
		Concurrency: concurrency,
		Elapsed:     time.Since(start),
		Metrics:     metrics,
	}
}
