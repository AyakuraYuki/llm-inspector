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

func TestPercentileStats_FullRange(t *testing.T) {
	// 1..10ms 十个样本：分位数用最近秩（idx = ceil(n*p)-1），
	// StdDev 为总体标准差 sqrt(mean((x-5.5)^2)) = sqrt(8.25) ≈ 2.8723ms。
	var durations []time.Duration
	for i := 1; i <= 10; i++ {
		durations = append(durations, time.Duration(i)*time.Millisecond)
	}
	s := percentileStats(durations)

	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"Min", s.Min, 1 * time.Millisecond},
		{"P10", s.P10, 1 * time.Millisecond},
		{"P25", s.P25, 3 * time.Millisecond},
		{"P50", s.P50, 5 * time.Millisecond},
		{"P75", s.P75, 8 * time.Millisecond},
		{"P90", s.P90, 9 * time.Millisecond},
		{"P95", s.P95, 10 * time.Millisecond},
		{"Max", s.Max, 10 * time.Millisecond},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if s.Avg != 5500*time.Microsecond {
		t.Errorf("Avg = %v, want 5.5ms", s.Avg)
	}
	if got := s.StdDev; got < 2870*time.Microsecond || got > 2875*time.Microsecond {
		t.Errorf("StdDev = %v, want ≈2.872ms", got)
	}
	if s.N != 10 {
		t.Errorf("N = %d, want 10", s.N)
	}
}

func TestPercentileStats_SingleSampleZeroStdDev(t *testing.T) {
	s := percentileStats([]time.Duration{42 * time.Millisecond})
	if s.Min != 42*time.Millisecond || s.Max != 42*time.Millisecond {
		t.Errorf("Min/Max = %v/%v, want 都是 42ms", s.Min, s.Max)
	}
	if s.StdDev != 0 {
		t.Errorf("StdDev = %v, want 0（单样本没有离散度）", s.StdDev)
	}
}

func TestFloatPercentileStats_FullRange(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	s := floatPercentileStats(values)
	if s.Min != 1 || s.Max != 10 {
		t.Errorf("Min/Max = %v/%v, want 1/10", s.Min, s.Max)
	}
	if s.P90 != 9 {
		t.Errorf("P90 = %v, want 9", s.P90)
	}
	if s.Avg != 5.5 {
		t.Errorf("Avg = %v, want 5.5", s.Avg)
	}
	if s.StdDev < 2.87 || s.StdDev > 2.88 {
		t.Errorf("StdDev = %v, want ≈2.872", s.StdDev)
	}
}

func TestAggregateMetrics_DecodeTPSFromAvgTPOT(t *testing.T) {
	// TTFT=10ms、E2E=210ms、200 token → gen_window=200ms，TPOT=1ms，
	// Decode tok/s = 1s/1ms = 1000。样本通过 ValidStreamTPS（200ms 窗口、1000 tok/s）。
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAI,
		Window:   time.Second,
		Metrics: []types.RequestMetrics{
			sample(10*time.Millisecond, 210*time.Millisecond, 200, true),
		},
	}
	agg := AggregateMetrics(result, types.SLOThresholds{}, false)
	if agg.TPOT.Avg != time.Millisecond {
		t.Fatalf("TPOT.Avg = %v, want 1ms", agg.TPOT.Avg)
	}
	if agg.DecodeTPS < 999 || agg.DecodeTPS > 1001 {
		t.Errorf("DecodeTPS = %v, want ≈1000", agg.DecodeTPS)
	}
}

func TestAggregateMetrics_DecodeTPSZeroWithoutTPOTSamples(t *testing.T) {
	// 图片端点没有 TPOT 概念，DecodeTPS 应保持 0（报表按 0 显示 N/A）。
	result := types.BenchmarkResult{
		Provider: types.ProviderOpenAIImage,
		Window:   time.Second,
		Metrics:  []types.RequestMetrics{{TotalLatency: time.Second, Success: true}},
	}
	agg := AggregateMetrics(result, types.SLOThresholds{}, false)
	if agg.DecodeTPS != 0 {
		t.Errorf("DecodeTPS = %v, want 0", agg.DecodeTPS)
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
