package metrics

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/tokstats"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// per-request 生成速率样本须经 tokstats.ValidStreamTPS 校验：
// 生成窗口双门槛（绝对下限 + 占 E2E 比例）剔除「整条响应缓冲后一次性到达」
// 的排空样本（µs~ms 级窗口测出的是排空速度而非解码速度，能虚高到千万
// tok/s 量级），单流物理天花板兜住任何漏网的虚高形态。
// 剔除只作用于 TPOT/TPS/TPM 分位数；系统级 TPS/TPM 按窗口总 token 计算，
// 时延分位数按 E2E 计算，均不受影响。剔除数记入 GenSpeedExcluded 供报表标注。

// AggregateMetrics 将原始请求结果聚合为汇聚指标。
// slo 为 goodput 判定阈值（slo.Enabled() 为 false 时不计算 goodput）；
// showHistogram 为真时额外计算 E2E/TTFT 延迟分布直方图。
func AggregateMetrics(result types.BenchmarkResult, slo types.SLOThresholds, showHistogram bool) types.AggregatedMetrics {
	agg := types.AggregatedMetrics{
		Model:        result.Model,
		Provider:     result.Provider,
		TokenGroup:   result.TokenGroup,
		Concurrency:  result.Concurrency,
		TargetRate:   result.TargetRate,
		Start:        result.Start,
		Elapsed:      result.Elapsed,
		Total:        len(result.Metrics),
		ErrorCounts:  make(map[types.ErrorType]int),
		StoppedEarly: result.StoppedEarly,
		RequestLimit: result.RequestLimit,
	}

	// 吞吐统计窗口：正常结束的档位取名义压测时长，提前中止的档位取实际运行时长。
	// deadline 前发出、deadline 后才完成的长尾请求仍计入时延分位数，但不计入
	// QPS/TPS——排空期内并发持续衰减，若按整个 Elapsed 摊分母会系统性低估吞吐。
	window := result.Elapsed
	if result.Window > 0 && result.Window < window {
		window = result.Window
	}
	agg.Window = window
	cutoff := result.Start.Add(window)

	// 稳态窗口：掐掉头尾各 10%，只算中间 80% 内完成的请求；Last 30s 只算窗口
	// 最后 30s 内完成的请求（窗口不足 30s 时不算）。都以「完成时刻」判定，与
	// Overall 的口径一致。Start 为零值（直接构造的测试样本）时无时间轴，跳过。
	hasTimeline := !result.Start.IsZero()
	steadyLo := result.Start.Add(window / 10)
	steadyHi := result.Start.Add(window - window/10)
	steadyWindow := steadyHi.Sub(steadyLo)
	last30Lo := cutoff.Add(-last30sWindow)
	hasLast30 := hasTimeline && window >= last30sWindow

	var (
		ttfts           []time.Duration
		tpots           []time.Duration
		itls            []time.Duration
		tpsValues       []float64
		iorValues       []float64
		cacheHitValues  []float64
		latencies       []time.Duration
		totalToks       int64
		totalInputToks  int64
		totalCachedToks int64
		cacheReported   int
		winSuccess      int
		winToks         int64
		goodCount       int
		steadySuccess   int
		steadyToks      int64
		last30Success   int
		last30Toks      int64
		driftAbs        []float64
	)

	isStreaming := result.Provider != types.ProviderOpenAIImage

	for _, m := range result.Metrics {
		if slo.Enabled() && isGood(m, slo, isStreaming) {
			goodCount++
		}

		if !m.Success {
			agg.Failed++
			if m.ErrorType != types.ErrorTypeNone {
				agg.ErrorCounts[m.ErrorType]++
			}
			agg.FailedDetails = append(agg.FailedDetails, m)
			continue
		}
		agg.Success++
		// 时延分位数只统计成功请求：快速失败（毫秒级 4xx）会拉低 P50，
		// 超时失败会拉爆 P99，混入后分布失真；失败时延保留在 FailedDetails 里。
		latencies = append(latencies, m.TotalLatency)
		totalToks += m.OutputTokens
		totalInputToks += m.InputTokens
		totalCachedToks += m.CachedInputTokens
		end := m.Timestamp.Add(m.TotalLatency)
		if !hasTimeline || !end.After(cutoff) {
			winSuccess++
			winToks += m.OutputTokens
		}
		if hasTimeline && end.After(steadyLo) && !end.After(steadyHi) {
			steadySuccess++
			steadyToks += m.OutputTokens
		}
		if hasLast30 && end.After(last30Lo) && !end.After(cutoff) {
			last30Success++
			last30Toks += m.OutputTokens
		}

		if isStreaming {
			if m.TTFT > 0 {
				ttfts = append(ttfts, m.TTFT)
			}
			// 与 Python 脚本对齐：TPOT = gen_window / output_tokens
			genWindow := m.TotalLatency - m.TTFT
			if m.OutputTokens > 0 {
				if tokstats.ValidStreamTPS(m.OutputTokens, genWindow, m.TotalLatency) {
					tpot := time.Duration(float64(genWindow) / float64(m.OutputTokens))
					tpots = append(tpots, tpot)

					// per-request TPS = output_tokens / gen_window_seconds
					tpsValues = append(tpsValues, float64(m.OutputTokens)/genWindow.Seconds())

					// ITL 剔除口径与 TPOT/TPS 保持一致：未通过速率有效性校验的
					// 请求（一次性到达/超出物理天花板）其内部事件间隔同样不可信。
					for _, ms := range m.ITLSamplesMS {
						itls = append(itls, time.Duration(ms*float64(time.Millisecond)))
					}
				} else {
					agg.GenSpeedExcluded++
				}
			}
			if m.OutputEstimated {
				agg.EstimatedOutputs++
			}
			// usage 对拍：LocalOutputTokens > 0 表示该请求完成了对拍
			if m.LocalOutputTokens > 0 {
				agg.UsageChecked++
				if m.UsageDrifted {
					agg.UsageDrifted++
				}
				driftAbs = append(driftAbs, math.Abs(m.UsageDriftPct))
			}
			// per-request 输入/输出 token 比
			if m.InputTokens > 0 && m.OutputTokens > 0 {
				iorValues = append(iorValues, float64(m.InputTokens)/float64(m.OutputTokens))
			}
			// per-request 缓存命中率（%），仅在 provider 上报了缓存字段时入样：
			// 未上报时 CachedInputTokens 恒为 0，入样会把分位数压成 0%，
			// 与「上报了但真实命中 0%」无法区分
			if m.CacheReported {
				cacheReported++
				if m.InputTokens > 0 {
					cacheHitValues = append(cacheHitValues, util.CacheHitRatio(m.CachedInputTokens, m.InputTokens))
				}
			}
		}
	}

	agg.TTFT = percentileStats(ttfts)
	agg.TPOT = percentileStats(tpots)
	agg.ITL = percentileStats(itls)
	agg.Latency = percentileStats(latencies)

	// Decode tok/s：平均 TPOT 取倒数，对标 evalscope 的同名指标。入样口径随
	// TPOT——未通过速率有效性校验的样本已被剔除，不会把排空速度算成解码速度。
	if agg.TPOT.Avg > 0 {
		agg.DecodeTPS = float64(time.Second) / float64(agg.TPOT.Avg)
	}

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

	// 系统级吞吐量：只统计吞吐窗口内完成的请求，分母为窗口时长
	if secs := window.Seconds(); secs > 0 {
		agg.QPS = float64(winSuccess) / secs
		agg.QPM = agg.QPS * 60
		if isStreaming && winToks > 0 {
			agg.TPS = float64(winToks) / secs
			agg.TPM = agg.TPS * 60
		}
	}

	// 稳态 / Last 30s 吞吐
	if hasTimeline {
		if secs := steadyWindow.Seconds(); secs > 0 {
			agg.SteadyWindow = steadyWindow
			agg.SteadyQPS = float64(steadySuccess) / secs
			if isStreaming {
				agg.SteadyTPS = float64(steadyToks) / secs
			}
		}
		if hasLast30 {
			secs := last30sWindow.Seconds()
			agg.Last30sWindow = last30sWindow
			agg.Last30sQPS = float64(last30Success) / secs
			if isStreaming {
				agg.Last30sTPS = float64(last30Toks) / secs
			}
		}
	}

	agg.UsageDriftAbs = floatPercentileStats(driftAbs)

	// 系统级输入/输出 token 比
	if totalToks > 0 {
		agg.IORatio = float64(totalInputToks) / float64(totalToks)
	}

	// 系统级缓存命中统计
	agg.TotalInputTokens = totalInputToks
	agg.TotalCachedTokens = totalCachedToks
	agg.CacheReportedCount = cacheReported
	if totalInputToks > 0 {
		agg.CacheHitRatio = util.CacheHitRatio(totalCachedToks, totalInputToks)
	}

	// goodput：仅当配置了至少一项 SLO 阈值才计算，否则 SLOConfigured 保持 false，
	// 报表借此区分「未配置」与「配置了但 0%」。
	if slo.Enabled() {
		agg.SLOConfigured = true
		agg.SLO = slo
		agg.GoodputCount = goodCount
		if agg.Total > 0 {
			agg.GoodputRatio = float64(goodCount) / float64(agg.Total) * 100
		}
	}

	// 延迟分布直方图：仅 showHistogram 开启时计算，避免给不需要的用户增加开销。
	if showHistogram {
		agg.E2EHistogram = Histogram(latencies, histogramBuckets)
		agg.TTFTHistogram = Histogram(ttfts, histogramBuckets)
	}

	return agg
}

// last30sWindow 是「Last 30s」吞吐窗口的长度，与 evalscope 的同名指标一致。
const last30sWindow = 30 * time.Second

// percentileStats 计算一组时延样本的 Min/P10/P25/P50/P75/P90/P95/P99/P99.5/P99.9/Max/Avg/StdDev。
func percentileStats(durations []time.Duration) types.PercentileStats {
	n := len(durations)
	if n == 0 {
		return types.PercentileStats{}
	}

	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	avg := total / time.Duration(n)

	pct := func(p float64) time.Duration {
		idx := max(int(math.Ceil(float64(n)*p))-1, 0)
		return sorted[idx]
	}

	return types.PercentileStats{
		Min:    sorted[0],
		P10:    pct(0.10),
		P25:    pct(0.25),
		P50:    pct(0.50),
		P75:    pct(0.75),
		P90:    pct(0.90),
		P95:    pct(0.95),
		P99:    pct(0.99),
		P995:   pct(0.995),
		P999:   pct(0.999),
		Max:    sorted[n-1],
		Avg:    avg,
		StdDev: stdDevDuration(sorted, avg),
		N:      n,
	}
}

// stdDevDuration 计算一组时延样本相对 avg 的总体标准差（分母 N）。
// 偏差先转 float64 再平方：纳秒量级的 Duration 平方会轻易溢出 int64
// （1s 的偏差平方已是 1e18，接近 int64 上限）。
func stdDevDuration(sorted []time.Duration, avg time.Duration) time.Duration {
	if len(sorted) < 2 {
		return 0
	}
	var sumSq float64
	for _, d := range sorted {
		diff := float64(d - avg)
		sumSq += diff * diff
	}
	return time.Duration(math.Sqrt(sumSq / float64(len(sorted))))
}

// floatPercentileStats 计算一组 float64 样本的分位数，档位与 percentileStats 一致。
func floatPercentileStats(values []float64) types.FloatStats {
	n := len(values)
	if n == 0 {
		return types.FloatStats{}
	}

	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)

	var total float64
	for _, v := range sorted {
		total += v
	}
	avg := total / float64(n)

	pct := func(p float64) float64 {
		idx := max(int(math.Ceil(float64(n)*p))-1, 0)
		return sorted[idx]
	}

	stdDev := 0.0
	if n >= 2 {
		var sumSq float64
		for _, v := range sorted {
			diff := v - avg
			sumSq += diff * diff
		}
		stdDev = math.Sqrt(sumSq / float64(n))
	}

	return types.FloatStats{
		Min:    sorted[0],
		P10:    pct(0.10),
		P25:    pct(0.25),
		P50:    pct(0.50),
		P75:    pct(0.75),
		P90:    pct(0.90),
		P95:    pct(0.95),
		P99:    pct(0.99),
		P995:   pct(0.995),
		P999:   pct(0.999),
		Max:    sorted[n-1],
		Avg:    avg,
		StdDev: stdDev,
		N:      n,
	}
}

// isGood 判定单条请求是否满足配置的 SLO 阈值（goodput 判定）。
// 失败请求必然不达标；成功请求逐项比较已配置（非零）的阈值，未配置的维度不参与判定。
// TTFT/TPOT 阈值仅在流式端点生效（图片生成端点没有这两个概念）。
func isGood(m types.RequestMetrics, slo types.SLOThresholds, isStreaming bool) bool {
	if !m.Success {
		return false
	}
	if slo.E2E > 0 && m.TotalLatency > slo.E2E {
		return false
	}
	if !isStreaming {
		return true
	}
	if slo.TTFT > 0 && m.TTFT > slo.TTFT {
		return false
	}
	if slo.TPOT > 0 {
		if m.OutputTokens <= 0 {
			return false // 拿不到 TPOT，保守判定为不达标
		}
		genWindow := m.TotalLatency - m.TTFT
		tpot := time.Duration(float64(genWindow) / float64(m.OutputTokens))
		if tpot > slo.TPOT {
			return false
		}
	}
	return true
}

// histogramBuckets 是延迟分布直方图的默认分桶数。
const histogramBuckets = 10

// Histogram 把一组时延样本按对数刻度分成 buckets 个桶，返回每桶的
// [Lo, Hi) 区间与样本数。对数刻度让长尾分布也能在有限桶数下均匀展开，
// 避免线性分桶时绝大多数样本都挤在第一个桶。样本为空时返回 nil。
func Histogram(durations []time.Duration, buckets int) []types.HistBucket {
	n := len(durations)
	if n == 0 || buckets <= 0 {
		return nil
	}

	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	slices.Sort(sorted)

	lo, hi := sorted[0], sorted[n-1]
	if lo <= 0 {
		lo = time.Microsecond
	}
	if hi < lo {
		hi = lo
	}

	logLo, logHi := math.Log(float64(lo)), math.Log(float64(hi))
	result := make([]types.HistBucket, buckets)
	if logHi <= logLo {
		// 全部样本落在同一个值（或差异小到对数刻度下不可分辨），单桶兜底。
		result[0] = types.HistBucket{Lo: lo, Hi: hi, Count: n}
		return result[:1]
	}

	step := (logHi - logLo) / float64(buckets)
	for i := range result {
		bucketLo := time.Duration(math.Exp(logLo + step*float64(i)))
		bucketHi := time.Duration(math.Exp(logLo + step*float64(i+1)))
		if i == buckets-1 {
			bucketHi = hi
		}
		result[i] = types.HistBucket{Lo: bucketLo, Hi: bucketHi}
	}
	for _, d := range sorted {
		idx := int((math.Log(float64(max(d, lo))) - logLo) / step)
		idx = min(max(idx, 0), buckets-1)
		result[idx].Count++
	}
	return result
}
