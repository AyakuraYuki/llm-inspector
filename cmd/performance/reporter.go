package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Reporter 接收压测过程中的事件，由具体实现决定如何展示（控制台或 TUI）。
// RequestDone 会在高并发的请求协程中被调用，实现必须保证轻量且并发安全。
type Reporter interface {
	PreflightStart(total int)
	PreflightResult(model ModelSpec, m RequestMetrics)
	PreflightEnd(allOK bool)
	WarmupStart(concurrency int, duration time.Duration)
	WarmupModel(idx, total int, model ModelSpec, deadline time.Time)
	WarmupEnd()
	LevelStart(seq, total int, model ModelSpec, concurrency int, deadline time.Time)
	RequestDone(m RequestMetrics)
	LevelEnd(agg AggregatedMetrics)
	CooldownStart(d time.Duration)
	BenchmarkEnd(aborted bool)
}

// levelCounters 保存当前档位的实时计数。requests/success 用 atomic 保证无锁写入；
// 错误类型细分数量较多（十余种）且集合固定，用 sync.Map 存 *atomic.Int64 既避免了
// 十几个具名字段的冗余，又不需要为每次自增加互斥锁，请求协程写入互不阻塞。
type levelCounters struct {
	requests atomic.Int64
	success  atomic.Int64
	byType   sync.Map // ErrorType -> *atomic.Int64
}

func (c *levelCounters) add(m RequestMetrics) {
	c.requests.Add(1)
	if m.Success {
		c.success.Add(1)
		return
	}
	if m.ErrorType == ErrorTypeNone {
		return
	}
	v, _ := c.byType.LoadOrStore(m.ErrorType, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (c *levelCounters) reset() {
	c.requests.Store(0)
	c.success.Store(0)
	c.byType.Range(func(k, _ any) bool {
		c.byType.Delete(k)
		return true
	})
}

// snapshot 返回一致性要求不高的瞬时读数：requests, success, 以及按错误类型的计数。
func (c *levelCounters) snapshot() (n, ok int64, byType map[ErrorType]int64) {
	byType = make(map[ErrorType]int64)
	c.byType.Range(func(k, v any) bool {
		byType[k.(ErrorType)] = v.(*atomic.Int64).Load()
		return true
	})
	return c.requests.Load(), c.success.Load(), byType
}

// formatErrorCounts 按 errorTypeOrder 固定顺序拼接非零的错误计数，用于进度/TUI 展示。
func formatErrorCounts(byType map[ErrorType]int64) string {
	var parts []string
	for _, et := range errorTypeOrder {
		if n := byType[et]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", et, n))
		}
	}
	return strings.Join(parts, " ")
}

// consoleReporter 复刻原有的纯文本控制台输出，用于非 TTY 环境或 -no-tui。
type consoleReporter struct {
	counters levelCounters
	deadline atomic.Int64 // 当前档位 deadline 的 UnixNano
	stopCh   chan struct{}
}

func (r *consoleReporter) PreflightStart(int) {
	fmt.Println("[预检] 连通性验证...")
}

func (r *consoleReporter) PreflightResult(model ModelSpec, m RequestMetrics) {
	if m.Success {
		fmt.Printf("  [OK]   %-34s (%s) → %.0fms\n",
			model.Name, string(model.Provider), float64(m.TotalLatency)/float64(time.Millisecond))
	} else {
		fmt.Printf("  [FAIL] %-34s (%s) → %s\n",
			model.Name, string(model.Provider), m.Error)
	}
}

func (r *consoleReporter) PreflightEnd(allOK bool) {
	if allOK {
		fmt.Println("[预检] 全部通过")
	}
}

func (r *consoleReporter) WarmupStart(concurrency int, duration time.Duration) {
	fmt.Printf("[预热] 并发=%d，时长=%s...\n", concurrency, duration)
}

func (r *consoleReporter) WarmupModel(_, _ int, model ModelSpec, _ time.Time) {
	fmt.Printf("  预热: %s (%s)\n", model.Name, string(model.Provider))
}

func (r *consoleReporter) WarmupEnd() {
	fmt.Println("[预热] 完成")
}

func (r *consoleReporter) LevelStart(seq, total int, model ModelSpec, concurrency int, deadline time.Time) {
	fmt.Printf("\n[%d/%d] Model=%-28s Provider=%-12s Concurrency=%d\n",
		seq, total, model.Name, string(model.Provider), concurrency)

	r.counters.reset()
	r.deadline.Store(deadline.UnixNano())

	// 进度汇报协程：每 10 s 打印一次进度和失败原因分布
	r.stopCh = make(chan struct{})
	go func(stopCh chan struct{}) {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n, ok, byType := r.counters.snapshot()
				remain := max(time.Until(time.Unix(0, r.deadline.Load())).Round(time.Second), 0)
				fmt.Printf("\r      progress: %d requests, %d failed (%s), %.0fs remaining   ",
					n, n-ok, formatErrorCounts(byType), remain.Seconds())
			case <-stopCh:
				return
			}
		}
	}(r.stopCh)
}

func (r *consoleReporter) RequestDone(m RequestMetrics) {
	r.counters.add(m)
}

func (r *consoleReporter) LevelEnd(agg AggregatedMetrics) {
	close(r.stopCh)
	fmt.Printf("\r%-140s\r", "") // 清除进度行

	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}
	fmt.Printf("      done: %d req, %d ok, %d failed (%.1f%% error)\n",
		agg.Total, agg.Success, agg.Failed, errPct)
}

func (r *consoleReporter) CooldownStart(d time.Duration) {
	fmt.Printf("      [冷却] 等待 %s 后进入下一并发档位...\n\n", d)
}

func (r *consoleReporter) BenchmarkEnd(aborted bool) {
	if aborted {
		fmt.Println("\n[中止] 收到中断信号，压测提前结束，以下为已完成部分的结果。")
	}
}
