package metrics

import (
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

func sample(ttft, total time.Duration, outputTokens int64, success bool) types.RequestMetrics {
	return types.RequestMetrics{
		TTFT: ttft, TotalLatency: total, OutputTokens: outputTokens,
		InputTokens: 100, Success: success,
	}
}

func TestAggregateMetrics_GoodputNotConfigured(t *testing.T) {
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAI,
		Window:   time.Second,
		Metrics:  []types.RequestMetrics{sample(10*time.Millisecond, 100*time.Millisecond, 50, true)},
	}
	agg := AggregateMetrics(result, types.SLOThresholds{}, false)
	if agg.SLOConfigured {
		t.Fatalf("SLOConfigured = true, want false when no SLO thresholds are set")
	}
	if agg.GoodputRatio != 0 {
		t.Errorf("GoodputRatio = %v, want 0 when not configured", agg.GoodputRatio)
	}
}

func TestAggregateMetrics_GoodputBoundary(t *testing.T) {
	slo := types.SLOThresholds{TTFT: 100 * time.Millisecond, E2E: 1 * time.Second}
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAI,
		Window:   time.Second,
		Metrics: []types.RequestMetrics{
			sample(100*time.Millisecond, 500*time.Millisecond, 50, true), // TTFT 恰好等于阈值，达标
			sample(150*time.Millisecond, 500*time.Millisecond, 50, true), // TTFT 超阈值，不达标
			sample(50*time.Millisecond, 1200*time.Millisecond, 50, true), // E2E 超阈值，不达标
			sample(0, 0, 0, false), // 失败请求，必然不达标
		},
	}
	agg := AggregateMetrics(result, slo, false)
	if !agg.SLOConfigured {
		t.Fatalf("SLOConfigured = false, want true")
	}
	if agg.GoodputCount != 1 {
		t.Errorf("GoodputCount = %d, want 1", agg.GoodputCount)
	}
	wantRatio := 25.0
	if agg.GoodputRatio != wantRatio {
		t.Errorf("GoodputRatio = %v, want %v", agg.GoodputRatio, wantRatio)
	}
}

func TestAggregateMetrics_GoodputTPOTUnavailableConservative(t *testing.T) {
	slo := types.SLOThresholds{TPOT: 20 * time.Millisecond}
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAI,
		Window:   time.Second,
		Metrics:  []types.RequestMetrics{sample(10*time.Millisecond, 100*time.Millisecond, 0, true)}, // OutputTokens=0，无法验证 TPOT
	}
	agg := AggregateMetrics(result, slo, false)
	if agg.GoodputCount != 0 {
		t.Errorf("GoodputCount = %d, want 0（TPOT 不可验证应保守判定为不达标）", agg.GoodputCount)
	}
}

func TestAggregateMetrics_GoodputImageEndpointSkipsTTFTTPOT(t *testing.T) {
	// 图片生成端点没有 TTFT/TPOT 概念，goodput 只按 E2E 判定。
	slo := types.SLOThresholds{TTFT: time.Millisecond, TPOT: time.Millisecond, E2E: time.Second}
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAIImage,
		Window:   time.Second,
		Metrics:  []types.RequestMetrics{{TotalLatency: 500 * time.Millisecond, Success: true}},
	}
	agg := AggregateMetrics(result, slo, false)
	if agg.GoodputCount != 1 {
		t.Errorf("GoodputCount = %d, want 1（图片端点不应因 TTFT/TPOT 阈值判定不达标）", agg.GoodputCount)
	}
}

func TestAggregateMetrics_ITLPoolingRespectsValidStreamTPSGate(t *testing.T) {
	valid := types.RequestMetrics{
		TTFT: 10 * time.Millisecond, TotalLatency: 500 * time.Millisecond,
		OutputTokens: 100, Success: true,
		ITLSamplesMS: []float64{5, 6, 7},
	}
	// 生成窗口极窄（一次性到达），应被 ValidStreamTPS 剔除，其 ITL 样本不应入池。
	invalid := types.RequestMetrics{
		TTFT: 499 * time.Millisecond, TotalLatency: 500 * time.Millisecond,
		OutputTokens: 1000, Success: true,
		ITLSamplesMS: []float64{999, 999},
	}
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAI,
		Window:   time.Second,
		Metrics:  []types.RequestMetrics{valid, invalid},
	}
	agg := AggregateMetrics(result, types.SLOThresholds{}, false)
	if agg.ITL.N != 3 {
		t.Fatalf("ITL.N = %d, want 3（只应池化通过速率有效性校验的请求的 ITL 样本）", agg.ITL.N)
	}
	if agg.GenSpeedExcluded != 1 {
		t.Errorf("GenSpeedExcluded = %d, want 1", agg.GenSpeedExcluded)
	}
}

func TestHistogram_Empty(t *testing.T) {
	if got := Histogram(nil, 10); got != nil {
		t.Errorf("Histogram(nil) = %v, want nil", got)
	}
}

func TestHistogram_BucketsCoverAllSamples(t *testing.T) {
	durations := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond,
		100 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second,
		2 * time.Second, 5 * time.Second,
	}
	buckets := Histogram(durations, 10)
	total := 0
	for _, b := range buckets {
		total += b.Count
	}
	if total != len(durations) {
		t.Errorf("bucket 计数总和 = %d, want %d（所有样本都应落入某个桶）", total, len(durations))
	}
	if buckets[0].Lo > durations[0] {
		t.Errorf("第一个桶下限 %v 大于最小样本 %v", buckets[0].Lo, durations[0])
	}
	if buckets[len(buckets)-1].Hi < durations[len(durations)-1] {
		t.Errorf("最后一个桶上限 %v 小于最大样本 %v", buckets[len(buckets)-1].Hi, durations[len(durations)-1])
	}
}

func TestHistogram_AllSameValueSingleBucket(t *testing.T) {
	durations := []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}
	buckets := Histogram(durations, 10)
	if len(buckets) != 1 {
		t.Fatalf("len(buckets) = %d, want 1（全部样本相同值时应单桶兜底）", len(buckets))
	}
	if buckets[0].Count != 3 {
		t.Errorf("buckets[0].Count = %d, want 3", buckets[0].Count)
	}
}
