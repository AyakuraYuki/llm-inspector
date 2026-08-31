package reporter

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// Reporter 接收压测过程中的事件，由具体实现决定如何展示（控制台或 TUI）。
// RequestDone 会在高并发的请求协程中被调用，实现必须保证轻量且并发安全。
type Reporter interface {
	PreflightStart(total int)
	PreflightResult(model types.ModelSpec, m types.RequestMetrics)
	PreflightEnd(allOK bool)
	WarmupStart(concurrency int, duration time.Duration)
	WarmupModel(idx, total int, model types.ModelSpec, deadline time.Time)
	WarmupEnd()
	LevelStart(seq, total int, model types.ModelSpec, concurrency int, deadline time.Time)
	RequestDone(m types.RequestMetrics)
	LevelEnd(agg types.AggregatedMetrics)
	EarlyStop(model types.ModelSpec, concurrency int, errRate float64)
	CooldownStart(d time.Duration)
	BenchmarkEnd(aborted bool)
}

// LevelCounters 保存当前档位的实时计数。requests/success 用 atomic 保证无锁写入；
// 错误类型细分数量较多（十余种）且集合固定，用 sync.Map 存 *atomic.Int64 既避免了
// 十几个具名字段的冗余，又不需要为每次自增加互斥锁，请求协程写入互不阻塞。
type LevelCounters struct {
	requests atomic.Int64
	success  atomic.Int64
	byType   sync.Map // types.ErrorType -> *atomic.Int64
}

func (c *LevelCounters) Add(m types.RequestMetrics) {
	c.requests.Add(1)
	if m.Success {
		c.success.Add(1)
		return
	}
	if m.ErrorType == types.ErrorTypeNone {
		return
	}
	v, _ := c.byType.LoadOrStore(m.ErrorType, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

func (c *LevelCounters) Reset() {
	c.requests.Store(0)
	c.success.Store(0)
	c.byType.Range(func(k, _ any) bool {
		c.byType.Delete(k)
		return true
	})
}

// Snapshot 返回一致性要求不高的瞬时读数：requests, success, 以及按错误类型的计数。
func (c *LevelCounters) Snapshot() (n, ok int64, byType map[types.ErrorType]int64) {
	byType = make(map[types.ErrorType]int64)
	c.byType.Range(func(k, v any) bool {
		byType[k.(types.ErrorType)] = v.(*atomic.Int64).Load()
		return true
	})
	return c.requests.Load(), c.success.Load(), byType
}

// formatErrorCounts 按 errorTypeOrder 固定顺序拼接非零的错误计数，用于进度/TUI 展示。
func formatErrorCounts(byType map[types.ErrorType]int64) string {
	var parts []string
	for _, et := range types.ErrorTypeOrder {
		if n := byType[et]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", et, n))
		}
	}
	return strings.Join(parts, " ")
}
