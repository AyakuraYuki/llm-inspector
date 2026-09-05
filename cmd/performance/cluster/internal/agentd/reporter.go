package agentd

import (
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// counterReporter 是 agent 侧的 reporter.Reporter 实现：RequestDone 只做
// 本地原子计数和错误样本记录，其余事件全部忽略——展示由 coordinator 端的
// TUI/Console 汇聚各 agent 的快照后统一完成，热路径不产生任何 RPC。
type counterReporter struct {
	counters reporter.LevelCounters
	samples  errSamples
}

func (r *counterReporter) RequestDone(m types.RequestMetrics) {
	r.counters.Add(m)
	if !m.Success && m.ErrorType != types.ErrorTypeNone && m.Error != "" {
		r.samples.record(m.ErrorType, m.Error)
	}
}

func (r *counterReporter) PreflightStart(int)                                    {}
func (r *counterReporter) PreflightResult(types.ModelSpec, types.RequestMetrics) {}
func (r *counterReporter) PreflightEnd(bool)                                     {}
func (r *counterReporter) WarmupStart(int, time.Duration)                        {}
func (r *counterReporter) WarmupModel(int, int, types.ModelSpec, time.Time)      {}
func (r *counterReporter) WarmupEnd()                                            {}
func (r *counterReporter) LevelStart(int, int, types.ModelSpec, int, time.Time)  {}
func (r *counterReporter) LevelEnd(types.AggregatedMetrics)                      {}
func (r *counterReporter) EarlyStop(types.ModelSpec, int, float64)               {}
func (r *counterReporter) CooldownStart(time.Duration)                           {}
func (r *counterReporter) BenchmarkEnd(bool)                                     {}

// errSamples 记录每种错误类型的最近一条错误信息，供 coordinator 的失败抽样
// 面板展示。与 TUI 的做法一致，按错误类型做 1s 限速：高失败率时锁竞争
// 不会拖慢请求协程，且样本本来只需要"最近一条"的新鲜度。
type errSamples struct {
	mu      sync.Mutex
	byType  map[types.ErrorType]string
	updated map[types.ErrorType]time.Time
}

const errSampleInterval = time.Second

func (s *errSamples) record(et types.ErrorType, msg string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byType == nil {
		s.byType = make(map[types.ErrorType]string)
		s.updated = make(map[types.ErrorType]time.Time)
	}
	if now.Sub(s.updated[et]) < errSampleInterval {
		return
	}
	s.byType[et] = msg
	s.updated[et] = now
}

// snapshot 返回样本的浅拷贝，供 progress 响应序列化。
func (s *errSamples) snapshot() map[types.ErrorType]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[types.ErrorType]string, len(s.byType))
	for k, v := range s.byType {
		out[k] = v
	}
	return out
}
