package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// mkResult 构造一个单档位结果：窗口足够长，所有请求都在窗口内完成。
func mkResult(ms ...types.RequestMetrics) types.BenchmarkResult {
	start := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	for i := range ms {
		ms[i].Timestamp = start
		ms[i].Success = true
	}
	return types.BenchmarkResult{
		Model:       "test-model",
		Provider:    types.ProviderOpenAI,
		Concurrency: 1,
		Start:       start,
		Window:      10 * time.Minute,
		Elapsed:     10 * time.Minute,
		Metrics:     ms,
	}
}

func TestAggregateMetrics_NormalSampleIncluded(t *testing.T) {
	// TTFT 1s，总时长 11s → 生成窗口 10s，1000 tokens → 100 tok/s
	agg := AggregateMetrics(mkResult(types.RequestMetrics{
		TTFT:         1 * time.Second,
		TotalLatency: 11 * time.Second,
		OutputTokens: 1000,
	}))

	if agg.TpsPr.N != 1 {
		t.Fatalf("TpsPr.N = %d, want 1", agg.TpsPr.N)
	}
	if got := agg.TpsPr.Avg; math.Abs(got-100) > 0.01 {
		t.Errorf("TpsPr.Avg = %f, want 100", got)
	}
	if agg.TPOT.N != 1 {
		t.Errorf("TPOT.N = %d, want 1", agg.TPOT.N)
	}
	if agg.GenSpeedExcluded != 0 {
		t.Errorf("GenSpeedExcluded = %d, want 0", agg.GenSpeedExcluded)
	}
}

func TestAggregateMetrics_BurstSampleExcluded(t *testing.T) {
	// 整条响应一次性到达：TTFT 贴着流结束，生成窗口仅 50µs，
	// 500 tokens / 50µs = 1000 万 tok/s——历史上正是打爆 TPM P99 的退化样本。
	burst := types.RequestMetrics{
		TTFT:         10*time.Second - 50*time.Microsecond,
		TotalLatency: 10 * time.Second,
		OutputTokens: 500,
	}
	normal := types.RequestMetrics{
		TTFT:         1 * time.Second,
		TotalLatency: 11 * time.Second,
		OutputTokens: 1000,
	}
	agg := AggregateMetrics(mkResult(burst, normal))

	if agg.GenSpeedExcluded != 1 {
		t.Fatalf("GenSpeedExcluded = %d, want 1", agg.GenSpeedExcluded)
	}
	if agg.TpsPr.N != 1 {
		t.Fatalf("TpsPr.N = %d, want 1（退化样本不应入样）", agg.TpsPr.N)
	}
	if got := agg.TpsPr.P99; got > 200 {
		t.Errorf("TpsPr.P99 = %f，退化样本泄漏进了分位数", got)
	}
	if agg.TPOT.N != 1 {
		t.Errorf("TPOT.N = %d, want 1（退化样本同样不应进 TPOT）", agg.TPOT.N)
	}

	// 剔除只影响速率分位数：时延分位数与成功计数仍包含该请求
	if agg.Success != 2 {
		t.Errorf("Success = %d, want 2", agg.Success)
	}
	if agg.Latency.N != 2 {
		t.Errorf("Latency.N = %d, want 2", agg.Latency.N)
	}
	if agg.TTFT.N != 2 {
		t.Errorf("TTFT.N = %d, want 2", agg.TTFT.N)
	}
}

func TestAggregateMetrics_FractionGateExcluded(t *testing.T) {
	// 绝对窗口超过 100ms 但只占 E2E 的 1%（TTFT ≥ 99% E2E）：
	// 大响应体经慢链路排空的形态，同样是一次性到达，应被比例门槛剔除。
	agg := AggregateMetrics(mkResult(types.RequestMetrics{
		TTFT:         29700 * time.Millisecond,
		TotalLatency: 30 * time.Second, // 生成窗口 300ms = 1% E2E
		OutputTokens: 3000,             // 名义 1 万 tok/s，物理不可能
	}))

	if agg.GenSpeedExcluded != 1 {
		t.Fatalf("GenSpeedExcluded = %d, want 1", agg.GenSpeedExcluded)
	}
	if agg.TpsPr.N != 0 {
		t.Errorf("TpsPr.N = %d, want 0", agg.TpsPr.N)
	}
}

func TestAggregateMetrics_SystemThroughputUnaffected(t *testing.T) {
	// System TPS/TPM 按窗口内完成的总 token ÷ 窗口时长计算，
	// 与 per-request 剔除无关：退化样本的 token 照常计入。
	burst := types.RequestMetrics{
		TTFT:         10*time.Second - 50*time.Microsecond,
		TotalLatency: 10 * time.Second,
		OutputTokens: 500,
	}
	normal := types.RequestMetrics{
		TTFT:         1 * time.Second,
		TotalLatency: 11 * time.Second,
		OutputTokens: 1000,
	}
	agg := AggregateMetrics(mkResult(burst, normal))

	wantTPS := 1500.0 / (10 * time.Minute).Seconds()
	if math.Abs(agg.TPS-wantTPS) > 1e-9 {
		t.Errorf("TPS = %f, want %f（剔除不得影响系统级吞吐）", agg.TPS, wantTPS)
	}
	if math.Abs(agg.TPM-wantTPS*60) > 1e-6 {
		t.Errorf("TPM = %f, want %f", agg.TPM, wantTPS*60)
	}
}
