package coord

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/proto"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/metrics"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// AgentReport 是单个 agent 在本次 run 中的概要，用于报表总览与收尾提示。
type AgentReport struct {
	Addr        string
	MaxShare    int // 该 agent 在整个 run 中的最大并发分片
	ErrlogCount int64
	ErrlogPath  string
}

// RunSummary 汇总本次分布式 run 的节点信息，供 main 写入 Excel 总览与打印提示。
type RunSummary struct {
	RunID  string
	Agents []AgentReport
}

// RunBenchmark 是分布式版的主循环，镜像单机 runner.RunBenchmark 的编排语义：
// 探活 → 会话建立 → 各机预检 → models × concurrency 逐档（切分下发、轮询聚合、
// 结果回收合并）→ 收尾。ctx 取消时优雅中止，返回已完成部分的结果。
func RunBenchmark(ctx context.Context, bench types.BenchmarkConfig, cluster *config.ClusterConfig, rep reporter.Reporter) ([]types.AggregatedMetrics, *RunSummary, error) {
	n := len(cluster.Agents)
	clients := make([]*Client, n)
	for i, addr := range cluster.Agents {
		clients[i] = NewClient(addr, cluster.AuthToken)
	}

	runID := time.Now().Format("20060102T150405")
	maxShares := Split(slices.Max(bench.Concurrency), n)
	summary := &RunSummary{RunID: runID, Agents: make([]AgentReport, n)}
	for i := range clients {
		summary.Agents[i] = AgentReport{Addr: clients[i].Addr, MaxShare: maxShares[i]}
	}

	// ── 探活 ──────────────────────────────────────────────────────────────
	if err := pingAll(ctx, clients); err != nil {
		return nil, summary, err
	}

	// ── 会话建立（连接池按各自最大分片一次性配置）─────────────────────────
	if err := forAllAgents(ctx, clients, 10*time.Second, func(reqCtx context.Context, i int, c *Client) error {
		return c.SessionStart(reqCtx, proto.SessionStart{
			Proto:               proto.Version,
			RunID:               runID,
			MaxLocalConcurrency: max(1, maxShares[i]),
		})
	}); err != nil {
		return nil, summary, fmt.Errorf("会话建立失败: %w", err)
	}
	// 收尾尽力而为：无论正常结束还是中途报错，都通知 agent 结束会话并回收 errlog 信息
	defer sessionEndAll(clients, summary)

	// ── 预检：每台 agent 各自验证到上游的网络路径 ─────────────────────────
	if err := preflightAll(ctx, clients, bench, runID, rep); err != nil {
		return nil, summary, err
	}

	// ── 正式测试 ──────────────────────────────────────────────────────────
	pollOpt := pollOptions{
		interval:     cluster.PollInterval,
		agentTimeout: cluster.AgentTimeout,
		earlyStop:    bench.EarlyStopEnabled,
		minSamples:   bench.MinSamples,
		maxErrorRate: bench.MaxErrorRate,
	}

	total := len(bench.Models) * len(bench.Concurrency)
	var results []types.AggregatedMetrics
	seq := 0

loop:
	for mi, model := range bench.Models {
		if bench.Warmup && ctx.Err() == nil {
			if err := runWarmup(ctx, clients, bench, model, mi, runID, pollOpt, rep); err != nil {
				rep.BenchmarkEnd(true)
				return results, summary, err
			}
		}
		for i, conc := range bench.Concurrency {
			if ctx.Err() != nil {
				break loop
			}
			seq++
			agg, stopped, err := runOneLevel(ctx, clients, bench, model, conc, runID, seq, total, pollOpt, rep)
			if err != nil {
				rep.BenchmarkEnd(true)
				return results, summary, err
			}
			results = append(results, agg)
			rep.LevelEnd(agg)

			if stopped {
				errRate := 0.0
				if agg.Total > 0 {
					errRate = float64(agg.Failed) / float64(agg.Total)
				}
				rep.EarlyStop(model, conc, errRate)
				if bench.SkipHigherConcurrency {
					// 跳过的档位计入进度序号，避免 [seq/total] 卡在半路不动
					seq += len(bench.Concurrency) - i - 1
					break
				}
			}

			if i < len(bench.Concurrency)-1 && bench.CooldownDuration > 0 && ctx.Err() == nil {
				rep.CooldownStart(bench.CooldownDuration)
				select {
				case <-ctx.Done():
				case <-time.After(bench.CooldownDuration):
				}
			}
		}
	}

	rep.BenchmarkEnd(ctx.Err() != nil)
	return results, summary, nil
}

// runOneLevel 执行一个 model×concurrency 档位：切分下发、轮询聚合、结果回收合并。
func runOneLevel(ctx context.Context, clients []*Client, bench types.BenchmarkConfig, model types.ModelSpec,
	conc int, runID string, seq, total int, pollOpt pollOptions, rep reporter.Reporter) (types.AggregatedMetrics, bool, error) {

	shares := Split(conc, len(clients))
	ramp := runner.RampDuration(conc, bench.Duration)
	t0 := time.Now()
	rep.LevelStart(seq, total, model, conc, t0.Add(bench.Duration))

	tasks, err := dispatchTasks(ctx, clients, shares, proto.TaskStart{
		RunID: runID,
		Kind:  proto.TaskLevel,
		Bench: levelBench(bench, model, bench.Duration),
		Model: model,
		Ramp:  ramp,
	}, fmt.Sprintf("%s-%03d", runID, seq))
	if err != nil {
		return types.AggregatedMetrics{}, false, err
	}

	stopped, err := pollLevel(ctx, tasks, pollOpt, rep)
	if err != nil {
		return types.AggregatedMetrics{}, false, err
	}

	// 结果回收：用户中止后原 ctx 已失效，换独立 ctx 收已完成部分
	collectCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		collectCtx, cancel = context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
	}
	parts := make([]types.BenchmarkResult, len(tasks))
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		collectErr error
	)
	for i, t := range tasks {
		wg.Go(func() {
			res, resErr := t.client.TaskResult(collectCtx, t.taskID)
			mu.Lock()
			defer mu.Unlock()
			if resErr != nil {
				if collectErr == nil {
					collectErr = resErr
				}
				return
			}
			parts[i] = RebaseResult(res, t0)
		})
	}
	wg.Wait()
	if collectErr != nil {
		return types.AggregatedMetrics{}, false, fmt.Errorf("档位结果回收失败: %w", collectErr)
	}

	merged := MergeLevel(t0, bench.Duration, conc, stopped, parts)
	merged.Model = model.Name
	merged.Provider = model.Provider
	merged.TokenGroup = model.TokenGroup
	return metrics.AggregateMetrics(merged), stopped, nil
}

// runWarmup 让每个 agent 按首个正式档位的分片并发预热，结果丢弃。
// 语义对齐单机版 warmupModel：紧贴各自模型的首档执行，把建连开销挡在测量窗口外。
func runWarmup(ctx context.Context, clients []*Client, bench types.BenchmarkConfig, model types.ModelSpec,
	mi int, runID string, pollOpt pollOptions, rep reporter.Reporter) error {

	warmupConc := bench.Concurrency[0]
	shares := Split(warmupConc, len(clients))
	rep.WarmupStart(warmupConc, bench.WarmupDuration)
	rep.WarmupModel(mi+1, len(bench.Models), model, time.Now().Add(bench.WarmupDuration))

	tasks, err := dispatchTasks(ctx, clients, shares, proto.TaskStart{
		RunID: runID,
		Kind:  proto.TaskWarmup,
		Bench: levelBench(bench, model, bench.WarmupDuration),
		Model: model,
		Ramp:  runner.RampDuration(warmupConc, bench.WarmupDuration),
	}, fmt.Sprintf("%s-warmup-%d", runID, mi))
	if err != nil {
		return err
	}

	// 预热不做早停判定，结果不回收（agent 侧已置空 Metrics）
	warmupOpt := pollOpt
	warmupOpt.earlyStop = false
	if _, err := pollLevel(ctx, tasks, warmupOpt, rep); err != nil {
		return err
	}
	rep.WarmupEnd()
	return nil
}

// dispatchTasks 并行向份额非零的 agent 下发任务。任一下发失败即广播取消已下发的分片。
func dispatchTasks(ctx context.Context, clients []*Client, shares []int, tmpl proto.TaskStart, taskIDPrefix string) ([]levelTask, error) {
	var tasks []levelTask
	for i, c := range clients {
		if shares[i] <= 0 {
			continue
		}
		tasks = append(tasks, levelTask{client: c, taskID: fmt.Sprintf("%s-%d", taskIDPrefix, i)})
	}

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		dispatchErr error
	)
	idx := 0
	for i := range clients {
		if shares[i] <= 0 {
			continue
		}
		t := tasks[idx]
		share := shares[i]
		idx++
		wg.Go(func() {
			req := tmpl
			req.TaskID = t.taskID
			req.Concurrency = share
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := t.client.TaskStart(reqCtx, req); err != nil {
				mu.Lock()
				if dispatchErr == nil {
					dispatchErr = err
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if dispatchErr != nil {
		broadcastCancel(tasks)
		return nil, fmt.Errorf("任务下发失败: %w", dispatchErr)
	}
	return tasks, nil
}

// levelBench 生成下发给 agent 的档位配置：只保留当前模型，指定档位时长，
// 关闭本地早停（统一由 coordinator 汇总全局错误率决策）。
func levelBench(bench types.BenchmarkConfig, model types.ModelSpec, duration time.Duration) types.BenchmarkConfig {
	b := bench
	b.Models = []types.ModelSpec{model}
	b.Duration = duration
	b.EarlyStopEnabled = false
	return b
}

// pingAll 并行探活：网络可达、协议版本一致、当前空闲，三者缺一不可。
func pingAll(ctx context.Context, clients []*Client) error {
	return forAllAgents(ctx, clients, 10*time.Second, func(reqCtx context.Context, _ int, c *Client) error {
		info, err := c.Ping(reqCtx)
		if err != nil {
			return err
		}
		if info.Proto != proto.Version {
			return fmt.Errorf("agent %s 协议版本不匹配：coordinator=%d agent=%d", c.Addr, proto.Version, info.Proto)
		}
		if info.Busy {
			return fmt.Errorf("agent %s 忙碌（任务 %s 运行中）", c.Addr, info.TaskID)
		}
		return nil
	})
}

// preflightAll 并行让每个 agent 预检全部模型，任一模型在任一 agent 上失败即中止。
func preflightAll(ctx context.Context, clients []*Client, bench types.BenchmarkConfig, runID string, rep reporter.Reporter) error {
	rep.PreflightStart(len(bench.Models) * len(clients))

	// 每台 agent 串行预检全部模型，超时预算按模型数 × 单模型预检上限（2min）放宽
	timeout := time.Duration(len(bench.Models))*2*time.Minute + time.Minute

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		allOK = true
	)
	for _, c := range clients {
		wg.Go(func() {
			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			resp, err := c.Preflight(reqCtx, proto.PreflightRequest{RunID: runID, Bench: bench})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				allOK = false
				rep.PreflightResult(types.ModelSpec{Name: "(agent) " + c.Addr}, types.RequestMetrics{
					Success: false, Error: err.Error(),
				})
				return
			}
			for _, r := range resp.Results {
				// 模型名标注 agent 地址，报表里能看出是哪台机器的网络路径有问题
				spec := types.ModelSpec{
					Name:       r.ModelName + " @" + c.Addr,
					Provider:   r.Provider,
					TokenGroup: r.TokenGroup,
				}
				rep.PreflightResult(spec, r.Metric)
				if !r.Metric.Success {
					allOK = false
				}
			}
		})
	}
	wg.Wait()

	rep.PreflightEnd(allOK)
	if !allOK {
		return fmt.Errorf("部分模型连通性验证失败，请检查上游渠道配置与各 agent 的网络路径后重试")
	}
	return nil
}

// sessionEndAll 尽力通知全部 agent 结束会话，并把各自的 errlog 信息写回 summary。
// 使用独立 ctx：用户中止后原 ctx 已失效，但收尾仍要完成。
func sessionEndAll(clients []*Client, summary *RunSummary) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Go(func() {
			if resp, err := c.SessionEnd(ctx); err == nil {
				summary.Agents[i].ErrlogCount = resp.ErrlogCount
				summary.Agents[i].ErrlogPath = resp.ErrlogPath
			}
		})
	}
	wg.Wait()
}

// forAllAgents 并行对每个 agent 执行 fn，返回第一个错误。
func forAllAgents(ctx context.Context, clients []*Client, timeout time.Duration, fn func(ctx context.Context, i int, c *Client) error) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for i, c := range clients {
		wg.Go(func() {
			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := fn(reqCtx, i, c); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return firstErr
}
