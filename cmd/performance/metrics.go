package main

import (
	"math"
	"sort"
	"time"
)

// percentileStats 计算一组时延样本的 P50/P95/P99/Avg/Min/Max。
func percentileStats(durations []time.Duration) PercentileStats {
	n := len(durations)
	if n == 0 {
		return PercentileStats{}
	}

	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	pct := func(p float64) time.Duration {
		idx := int(math.Ceil(float64(n)*p)) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}

	return PercentileStats{
		P50:  pct(0.50),
		P95:  pct(0.95),
		P99:  pct(0.99),
		P995: pct(0.995),
		P999: pct(0.999),
		Avg:  total / time.Duration(n),
		Min:  sorted[0],
		Max:  sorted[n-1],
		N:    n,
	}
}

// floatPercentileStats 计算一组 float64 样本的 P50/P95/P99/Avg。
func floatPercentileStats(values []float64) FloatStats {
	n := len(values)
	if n == 0 {
		return FloatStats{}
	}

	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)

	var total float64
	for _, v := range sorted {
		total += v
	}

	pct := func(p float64) float64 {
		idx := int(math.Ceil(float64(n)*p)) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}

	return FloatStats{
		P50:  pct(0.50),
		P95:  pct(0.95),
		P99:  pct(0.99),
		P995: pct(0.995),
		P999: pct(0.999),
		Avg:  total / float64(n),
		N:    n,
	}
}

// aggregateMetrics 将原始请求结果聚合为汇聚指标。
func aggregateMetrics(result BenchmarkResult) AggregatedMetrics {
	agg := AggregatedMetrics{
		Model:       result.Model,
		Provider:    result.Provider,
		Concurrency: result.Concurrency,
		Elapsed:     result.Elapsed,
		Total:       len(result.Metrics),
		ErrorCounts: make(map[ErrorType]int),
	}

	var (
		ttfts           []time.Duration
		tpots           []time.Duration
		tpsValues       []float64
		iorValues       []float64
		cacheHitValues  []float64
		latencies       []time.Duration
		totalToks       int64
		totalInputToks  int64
		totalCachedToks int64
	)

	isStreaming := result.Provider != ProviderOpenAIImage

	for _, m := range result.Metrics {
		latencies = append(latencies, m.TotalLatency)
		if !m.Success {
			agg.Failed++
			if m.ErrorType != ErrorTypeNone {
				agg.ErrorCounts[m.ErrorType]++
			}
			agg.FailedDetails = append(agg.FailedDetails, m)
			continue
		}
		agg.Success++
		totalToks += int64(m.OutputTokens)
		totalInputToks += int64(m.InputTokens)
		totalCachedToks += int64(m.CachedInputTokens)

		if isStreaming {
			if m.TTFT > 0 {
				ttfts = append(ttfts, m.TTFT)
			}
			// 与 Python 脚本对齐：TPOT = gen_window / output_tokens
			genWindow := m.TotalLatency - m.TTFT
			if m.OutputTokens > 0 && genWindow > 0 {
				tpot := time.Duration(float64(genWindow) / float64(m.OutputTokens))
				tpots = append(tpots, tpot)

				// per-request TPS = output_tokens / gen_window_seconds
				tpsValues = append(tpsValues, float64(m.OutputTokens)/genWindow.Seconds())
			}
			// per-request 输入/输出 token 比
			if m.InputTokens > 0 && m.OutputTokens > 0 {
				iorValues = append(iorValues, float64(m.OutputTokens)/float64(m.InputTokens))
			}
			// per-request 缓存命中率（%），仅在 provider 上报了缓存字段时有意义
			if m.InputTokens > 0 {
				cacheHitValues = append(cacheHitValues, float64(m.CachedInputTokens)/float64(m.InputTokens)*100)
			}
		}
	}

	agg.TTFT = percentileStats(ttfts)
	agg.TPOT = percentileStats(tpots)
	agg.Latency = percentileStats(latencies)

	// per-request TPS 分位数
	agg.TpsPr = floatPercentileStats(tpsValues)
	tpmValues := make([]float64, len(tpsValues))
	for i, v := range tpsValues {
		tpmValues[i] = v * 60
	}
	agg.TpmPr = floatPercentileStats(tpmValues)

	// per-request 输入/输出 token 比分位数
	agg.IOR = floatPercentileStats(iorValues)

	// per-request 缓存命中率分位数
	agg.CacheHitPr = floatPercentileStats(cacheHitValues)

	// 系统级吞吐量
	if secs := result.Elapsed.Seconds(); secs > 0 {
		agg.QPS = float64(agg.Success) / secs
		agg.QPM = agg.QPS * 60
		if isStreaming && totalToks > 0 {
			agg.TPS = float64(totalToks) / secs
			agg.TPM = agg.TPS * 60
		}
	}

	// 系统级输入/输出 token 比
	if totalInputToks > 0 {
		agg.IORatio = float64(totalToks) / float64(totalInputToks)
	}

	// 系统级缓存命中统计
	agg.TotalInputTokens = totalInputToks
	agg.TotalCachedTokens = totalCachedToks
	if totalInputToks > 0 {
		agg.CacheHitRatio = float64(totalCachedToks) / float64(totalInputToks) * 100
	}

	return agg
}
