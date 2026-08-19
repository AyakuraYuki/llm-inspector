package reporter

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// ConsoleReporter 复刻原有的纯文本控制台输出，用于非 TTY 环境或 -no-tui。
type ConsoleReporter struct {
	counters LevelCounters
	deadline atomic.Int64 // 当前档位 deadline 的 UnixNano
	stopCh   chan struct{}
}

func (r *ConsoleReporter) PreflightStart(int) {
	fmt.Println("[预检] 连通性验证...")
}

func (r *ConsoleReporter) PreflightResult(model types.ModelSpec, m types.RequestMetrics) {
	if m.Success {
		fmt.Printf("  [OK]   %-34s (%s, group=%s) → %.0fms\n",
			model.Name, string(model.Provider), model.TokenGroup, float64(m.TotalLatency)/float64(time.Millisecond))
	} else {
		fmt.Printf("  [FAIL] %-34s (%s, group=%s) → %s\n",
			model.Name, string(model.Provider), model.TokenGroup, m.Error)
	}
}

func (r *ConsoleReporter) PreflightEnd(allOK bool) {
	if allOK {
		fmt.Println("[预检] 全部通过")
	}
}

func (r *ConsoleReporter) WarmupStart(concurrency int, duration time.Duration) {
	fmt.Printf("[预热] 并发=%d，时长=%s...\n", concurrency, duration)
}

func (r *ConsoleReporter) WarmupModel(_, _ int, model types.ModelSpec, _ time.Time) {
	fmt.Printf("  预热: %s (%s, group=%s)\n", model.Name, string(model.Provider), model.TokenGroup)
}

func (r *ConsoleReporter) WarmupEnd() {
	fmt.Println("[预热] 完成")
}

func (r *ConsoleReporter) LevelStart(seq, total int, model types.ModelSpec, concurrency int, deadline time.Time) {
	fmt.Printf("\n[%d/%d] Model=%-28s Provider=%-12s Group=%-16s Concurrency=%d\n",
		seq, total, model.Name, string(model.Provider), model.TokenGroup, concurrency)

	r.counters.Reset()
	r.deadline.Store(deadline.UnixNano())

	// 进度汇报协程：每 10 s 打印一次进度和失败原因分布
	r.stopCh = make(chan struct{})
	go func(stopCh chan struct{}) {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n, ok, byType := r.counters.Snapshot()
				remain := max(time.Until(time.Unix(0, r.deadline.Load())).Round(time.Second), 0)
				fmt.Printf("\r      progress: %d requests, %d failed (%s), %.0fs remaining   ",
					n, n-ok, formatErrorCounts(byType), remain.Seconds())
			case <-stopCh:
				return
			}
		}
	}(r.stopCh)
}

func (r *ConsoleReporter) RequestDone(m types.RequestMetrics) {
	r.counters.Add(m)
}

func (r *ConsoleReporter) LevelEnd(agg types.AggregatedMetrics) {
	close(r.stopCh)
	fmt.Printf("\r%-140s\r", "") // 清除进度行

	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}
	fmt.Printf("      done: %d req, %d ok, %d failed (%.1f%% error)\n",
		agg.Total, agg.Success, agg.Failed, errPct)
}

func (r *ConsoleReporter) CooldownStart(d time.Duration) {
	fmt.Printf("      [冷却] 等待 %s 后进入下一并发档位...\n\n", d)
}

func (r *ConsoleReporter) BenchmarkEnd(aborted bool) {
	if aborted {
		fmt.Println("\n[中止] 收到中断信号，压测提前结束，以下为已完成部分的结果。")
	}
}
