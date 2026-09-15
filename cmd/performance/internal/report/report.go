package report

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	// colWidth 是终端报告的分隔线宽度。100 而非 80 是为了容纳分位数表新增的
	// P90/StdDev 两列——低分位与标准差是和 evalscope 十分位表对标的最小必要集。
	colWidth     = 100
	histBarWidth = 30 // ASCII 直方图条形长度（字符数），按各桶计数占最大计数的比例归一化
)

func PrintReport(results []types.AggregatedMetrics) {
	fmt.Printf("\n%s\n", strings.Repeat("=", colWidth))
	fmt.Printf("  BENCHMARK RESULTS\n")
	fmt.Printf("%s\n", strings.Repeat("=", colWidth))

	for _, agg := range results {
		printOne(agg)
	}

	printSummaryTable(results) // summary 表结尾
}

func printOne(agg types.AggregatedMetrics) {
	isStreaming := agg.Provider != types.ProviderOpenAIImage
	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}

	fmt.Printf("\n%s\n", strings.Repeat("-", colWidth))
	fmt.Printf("  Model: %s  |  Provider: %s  |  Token Group: %s  |  Concurrency: %d%s\n",
		agg.Model, agg.Provider, agg.TokenGroup, agg.Concurrency, targetRateSuffix(agg.TargetRate))
	fmt.Printf("  Elapsed: %s  |  Window: %s  |  Requests: %d total, %d ok, %d failed (%.1f%% error)\n",
		formatDuration(agg.Elapsed), formatDuration(agg.Window), agg.Total, agg.Success, agg.Failed, errPct)
	fmt.Printf("%s\n", strings.Repeat("-", colWidth))

	hdr := fmt.Sprintf("  %-16s  %-11s  %-11s  %-11s  %-11s  %-11s  %-11s  %s",
		"Metric", "P50", "P90", "P95", "P99", "Avg", "StdDev", "N")
	fmt.Println(hdr)
	fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))

	if isStreaming {
		printRow("TTFT", agg.TTFT)
		printRow("TPOT", agg.TPOT)
		printRow("ITL", agg.ITL)
		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))
		fmt.Printf("  TPS: %8.2f tok/s  |  TPM: %8.1f tok/min  |  QPS: %.4f req/s  |  QPM: %.2f req/min  |  I/O Ratio: %s\n",
			agg.TPS, agg.TPM, agg.QPS, agg.QPM, formatRatio(agg.IORatio))
		if agg.DecodeTPS > 0 {
			fmt.Printf("  Decode: %.1f tok/s (单流解码速度，1/平均 TPOT)\n", agg.DecodeTPS)
		}
	} else {
		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))
		fmt.Printf("  QPS: %.4f req/s  |  QPM: %.2f req/min\n", agg.QPS, agg.QPM)
	}

	// goodput：仅当本次运行配置了 SLO 阈值才输出，未配置时不产生任何新内容
	if agg.SLOConfigured {
		fmt.Printf("  Goodput: %.1f%% (SLO: %s)\n", agg.GoodputRatio, sloSummary(agg, isStreaming))
	}

	// 延迟分布直方图：仅 show_histogram 开启时才有数据，未开启时两个切片都是 nil
	if len(agg.E2EHistogram) > 0 {
		printHistogram("E2E Latency", agg.E2EHistogram)
	}
	if isStreaming && len(agg.TTFTHistogram) > 0 {
		printHistogram("TTFT", agg.TTFTHistogram)
	}

	// 吞吐口径按"窗口内完成"计数，E2E 时延占窗口比例过高时结果偏低，需醒目提示
	if note := throughputBiasNote(agg); note != "" {
		fmt.Printf("  [WARN] %s\n", note)
	}

	// 档位因错误率超过 early_stop 阈值被提前终止，未跑满设定时长
	if agg.StoppedEarly {
		fmt.Printf("  [WARN] 本档位因错误率超阈值被提前终止，未跑满设定时长\n")
	}

	// 未通过有效性校验的样本（生成窗口过窄/一次性到达，或超出单流物理天花板）
	// 不参与 TPOT/TPS/TPM 分位数
	if agg.GenSpeedExcluded > 0 {
		fmt.Printf("  [NOTE] %d 条样本未通过速率有效性校验（响应一次性到达或超出单流物理上限）被剔除出 TPOT/TPS 分位数，疑似网关缓冲、压测机读流饥饿或 usage 虚报\n",
			agg.GenSpeedExcluded)
	}

	// token 数为文本估算的样本占比过高时，速率分位数可信度下降
	if agg.EstimatedOutputs > 0 {
		fmt.Printf("  [NOTE] %d/%d 条成功样本的 token 数为文本估算（provider 未上报 usage），TPS/TPM 分位数可信度下降\n",
			agg.EstimatedOutputs, agg.Success)
	}

	// 失败原因分类
	if agg.Failed > 0 && len(agg.ErrorCounts) > 0 {
		var parts []string
		for _, et := range types.ErrorTypeOrder {
			if n, ok := agg.ErrorCounts[et]; ok && n > 0 {
				parts = append(parts, fmt.Sprintf("%s: %d", et, n))
			}
		}
		if len(parts) > 0 {
			fmt.Printf("  Error types: %s\n", strings.Join(parts, "  |  "))
		}
	}

	// 样本量过少时给出警告
	if n := agg.TTFT.N; n > 0 && n < 20 {
		fmt.Printf("  [WARN] low sample count (N=%d): P95/P99 may be inaccurate\n", n)
	}
}

func printRow(label string, s types.PercentileStats) {
	n := "-"
	if s.N > 0 {
		n = fmt.Sprintf("%d", s.N)
	}
	// StdDev 用 formatStdDev 而非 formatDuration：单样本的标准差 0 是有意义的
	// 结果（"没有离散度"），不能和"没有数据"一样显示成 N/A。
	fmt.Printf("  %-16s  %-11s  %-11s  %-11s  %-11s  %-11s  %-11s  %s\n",
		label,
		formatDuration(s.P50),
		formatDuration(s.P90),
		formatDuration(s.P95),
		formatDuration(s.P99),
		formatDuration(s.Avg),
		formatStdDev(s.StdDev, s.N),
		n,
	)
}

// formatStdDev 渲染标准差：无样本时 N/A，有样本时即使为 0 也照实显示。
func formatStdDev(d time.Duration, n int) string {
	if n == 0 {
		return "N/A"
	}
	if d == 0 {
		return "0ms"
	}
	return formatDuration(d)
}

func printSummaryTable(results []types.AggregatedMetrics) {
	fmt.Printf("\n%s\n", strings.Repeat("=", colWidth))
	fmt.Println("  SUMMARY TABLE")
	fmt.Printf("%s\n", strings.Repeat("-", colWidth))
	fmt.Printf("  %-22s  %-16s  %-5s  %-8s  %-8s  %-10s  %-10s  %-10s\n",
		"Model (Provider)", "Token Group", "Conc", "QPS", "TPS", "TTFT P50", "TTFT P95", "I/O Ratio")
	fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))

	for _, agg := range results {
		label := fmt.Sprintf("%s (%s)", agg.Model, agg.Provider)
		if len(label) > 22 {
			label = label[:19] + "..."
		}
		group := agg.TokenGroup
		if len(group) > 16 {
			group = group[:13] + "..."
		}
		tpsStr := "N/A"
		if agg.TPS > 0 {
			tpsStr = fmt.Sprintf("%.1f", agg.TPS)
		}
		fmt.Printf("  %-22s  %-16s  %-5d  %-8.3f  %-8s  %-10s  %-10s  %-10s\n",
			label,
			group,
			agg.Concurrency,
			agg.QPS,
			tpsStr,
			formatDuration(agg.TTFT.P50),
			formatDuration(agg.TTFT.P95),
			formatRatio(agg.IORatio),
		)
	}

	fmt.Printf("%s\n\n", strings.Repeat("=", colWidth))
}

// throughputBiasNote 在平均 E2E 时延占吞吐窗口比例过高时返回警告文案。
// QPS/TPS 只统计"窗口内完成"的请求,而压测从零起步,窗口前段约一个平均时延内
// 不可能有任何请求完成,吞吐因此被系统性低估约 Avg/Window 的比例;
// E2E 逼近窗口时 QPS/TPS 会趋近 0,只能通过加大 duration 稀释偏差。
func throughputBiasNote(agg types.AggregatedMetrics) string {
	if agg.Window <= 0 || agg.Latency.Avg <= 0 {
		return ""
	}
	ratio := float64(agg.Latency.Avg) / float64(agg.Window)
	if ratio < 0.2 {
		return ""
	}
	return fmt.Sprintf("平均 E2E 时延达吞吐窗口的 %.0f%%，QPS/TPS 被系统性低估约同等比例，建议加大 duration 后重测", ratio*100)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "N/A"
	}
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.0fµs", float64(d)/float64(time.Microsecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// formatRatio 格式化输入/输出 token 比，无数据时返回 "N/A"。
func formatRatio(r float64) string {
	if r <= 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.3f", r)
}

// targetRateSuffix 渲染 open-loop 档位的目标 RPS 后缀；closed-loop（TargetRate<=0）返回空字符串。
func targetRateSuffix(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return fmt.Sprintf("  |  Target Rate: %.2f req/s (open-loop)", rate)
}

// sloSummary 渲染 goodput 判定用的 SLO 阈值摘要，只展示已配置（非零）的维度。
func sloSummary(agg types.AggregatedMetrics, isStreaming bool) string {
	var parts []string
	if isStreaming && agg.SLO.TTFT > 0 {
		parts = append(parts, fmt.Sprintf("TTFT<=%s", formatDuration(agg.SLO.TTFT)))
	}
	if isStreaming && agg.SLO.TPOT > 0 {
		parts = append(parts, fmt.Sprintf("TPOT<=%s", formatDuration(agg.SLO.TPOT)))
	}
	if agg.SLO.E2E > 0 {
		parts = append(parts, fmt.Sprintf("E2E<=%s", formatDuration(agg.SLO.E2E)))
	}
	return strings.Join(parts, ", ")
}

// printHistogram 打印一组分桶的 ASCII 条形图，条形长度按各桶计数占最大计数的
// 比例归一化到 histBarWidth。全零计数（理论上不会发生，metrics.Histogram
// 保证至少一个桶非空）时不打印。
func printHistogram(title string, buckets []types.HistBucket) {
	maxCount := 0
	for _, b := range buckets {
		maxCount = max(maxCount, b.Count)
	}
	if maxCount == 0 {
		return
	}
	fmt.Printf("  %s 分布:\n", title)
	for _, b := range buckets {
		barLen := int(math.Round(float64(b.Count) / float64(maxCount) * histBarWidth))
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", histBarWidth-barLen)
		fmt.Printf("    %10s - %-10s | %s %d\n", formatDuration(b.Lo), formatDuration(b.Hi), bar, b.Count)
	}
}
