package runner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// noopReporter 是仅用于测试的 reporter.Reporter 空实现，除 RequestDone 外都不做任何事。
type noopReporter struct {
	done atomic.Int64
}

func (r *noopReporter) PreflightStart(int)                                    {}
func (r *noopReporter) PreflightResult(types.ModelSpec, types.RequestMetrics) {}
func (r *noopReporter) PreflightEnd(bool)                                     {}
func (r *noopReporter) WarmupStart(int, time.Duration)                        {}
func (r *noopReporter) WarmupModel(int, int, types.ModelSpec, time.Time)      {}
func (r *noopReporter) WarmupEnd()                                            {}
func (r *noopReporter) LevelStart(int, int, types.ModelSpec, int, time.Time)  {}
func (r *noopReporter) RequestDone(types.RequestMetrics)                      { r.done.Add(1) }
func (r *noopReporter) LevelEnd(types.AggregatedMetrics)                      {}
func (r *noopReporter) EarlyStop(types.ModelSpec, int, float64)               {}
func (r *noopReporter) CooldownStart(time.Duration)                           {}
func (r *noopReporter) BenchmarkEnd(bool)                                     {}

var _ reporter.Reporter = (*noopReporter)(nil)

// registerFakeProvider 临时把 provider 映射到一个立即成功、耗时可控的假请求函数，
// 返回一个 defer 用的清理函数，测试结束后恢复 doSSERequests 原状。
func registerFakeProvider(p types.Provider, delay time.Duration) func() {
	doSSERequests[p] = func(ctx context.Context, _ types.BenchmarkConfig, _ types.ModelSpec) types.RequestMetrics {
		if delay > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
		}
		return types.RequestMetrics{Success: true, TotalLatency: delay}
	}
	return func() { delete(doSSERequests, p) }
}

func TestRunLevel_ClosedLoopTargetRateZero(t *testing.T) {
	const provider = types.Provider("__test_closed__")
	defer registerFakeProvider(provider, 0)()

	cfg := types.BenchmarkConfig{Duration: 200 * time.Millisecond}
	model := types.ModelSpec{Name: "m", Provider: provider}
	rep := &noopReporter{}

	result := RunLevel(context.Background(), cfg, model, 5, 0, 0, rep)
	if result.TargetRate != 0 {
		t.Errorf("TargetRate = %v, want 0 for closed-loop", result.TargetRate)
	}
	if len(result.Metrics) == 0 {
		t.Fatalf("closed-loop 应至少发出若干请求，got 0")
	}
}

func TestRunLevel_OpenLoopArrivalRateApproximatesTarget(t *testing.T) {
	const provider = types.Provider("__test_open__")
	defer registerFakeProvider(provider, 0)()

	const (
		targetRate = 100.0 // req/s
		duration   = 500 * time.Millisecond
	)
	cfg := types.BenchmarkConfig{Duration: duration}
	model := types.ModelSpec{Name: "m", Provider: provider}
	rep := &noopReporter{}

	result := RunLevel(context.Background(), cfg, model, 50, targetRate, 0, rep)
	if result.TargetRate != targetRate {
		t.Errorf("TargetRate = %v, want %v", result.TargetRate, targetRate)
	}

	got := len(result.Metrics)
	want := targetRate * duration.Seconds() // 期望到达数 ≈ rate × duration（泊松过程均值）
	// 宽松容差（±60%）：avoid flaky——单次运行的到达数服从方差 = mean 的泊松分布，
	// 这里只验证量级正确，不追求统计精确。
	if float64(got) < want*0.4 || float64(got) > want*1.6 {
		t.Errorf("open-loop 请求数 = %d，期望约 %.0f（目标 RPS=%.0f × %s），偏差过大", got, want, targetRate, duration)
	}
}

func TestRunLevel_ThinkTimeCappedAtDeadline(t *testing.T) {
	const provider = types.Provider("__test_think__")
	defer registerFakeProvider(provider, 0)()

	// 思考时间远大于档位时长：worker 发完第一个请求后必须只等到 deadline 就收工，
	// 而不是睡满 10s——否则档位的排空期会被思考时间白白拉长。
	cfg := types.BenchmarkConfig{
		Duration:  150 * time.Millisecond,
		ThinkTime: 10 * time.Second,
	}
	model := types.ModelSpec{Name: "m", Provider: provider}

	start := time.Now()
	result := RunLevel(context.Background(), cfg, model, 1, 0, 0, &noopReporter{})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("档位实际耗时 %v，远超 duration 150ms：思考时间未截断到 deadline", elapsed)
	}
	if len(result.Metrics) == 0 {
		t.Error("至少应完成一个请求")
	}
}

func TestRunLevel_ThinkTimeReducesThroughput(t *testing.T) {
	const provider = types.Provider("__test_think_rate__")
	defer registerFakeProvider(provider, 0)()

	model := types.ModelSpec{Name: "m", Provider: provider}
	const duration = 300 * time.Millisecond

	noWait := RunLevel(context.Background(),
		types.BenchmarkConfig{Duration: duration}, model, 1, 0, 0, &noopReporter{})
	withWait := RunLevel(context.Background(),
		types.BenchmarkConfig{Duration: duration, ThinkTime: 50 * time.Millisecond}, model, 1, 0, 0, &noopReporter{})

	// 假 provider 零耗时，所以 think_time=0 能发出的请求数远多于 50ms 间隔的情况
	//（后者上限约 duration/think_time = 6 个）。只比较量级，不断言精确条数。
	if len(withWait.Metrics) >= len(noWait.Metrics) {
		t.Errorf("think_time=50ms 发出 %d 个请求，think_time=0 发出 %d 个：思考时间未生效",
			len(withWait.Metrics), len(noWait.Metrics))
	}
}

func TestShouldStopEarly(t *testing.T) {
	cases := []struct {
		name         string
		total        int64
		failed       int64
		minSamples   int
		maxErrorRate float64
		want         bool
	}{
		{"样本不足不触发", 10, 10, 20, 0.5, false},
		{"样本达标但错误率未超阈值", 20, 5, 20, 0.5, false},
		{"样本达标且错误率超阈值", 20, 11, 20, 0.5, true},
		{"样本达标且错误率恰好等于阈值不触发", 20, 10, 20, 0.5, false},
		{"全部失败必然触发", 20, 20, 20, 0.5, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ShouldStopEarly(c.total, c.failed, c.minSamples, c.maxErrorRate)
			if got != c.want {
				t.Errorf("ShouldStopEarly(%d, %d, %d, %v) = %v, want %v",
					c.total, c.failed, c.minSamples, c.maxErrorRate, got, c.want)
			}
		})
	}
}
