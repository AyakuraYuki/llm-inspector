package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const colWidth = 80

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
	fmt.Printf("  Model: %s  |  Provider: %s  |  Token Group: %s  |  Concurrency: %d\n",
		agg.Model, agg.Provider, agg.TokenGroup, agg.Concurrency)
	fmt.Printf("  Elapsed: %s  |  Window: %s  |  Requests: %d total, %d ok, %d failed (%.1f%% error)\n",
		formatDuration(agg.Elapsed), formatDuration(agg.Window), agg.Total, agg.Success, agg.Failed, errPct)
	fmt.Printf("%s\n", strings.Repeat("-", colWidth))

	hdr := fmt.Sprintf("  %-16s  %-11s  %-11s  %-11s  %-11s  %s",
		"Metric", "P50", "P95", "P99", "Avg", "N")
	fmt.Println(hdr)
	fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))

	if isStreaming {
		printRow("TTFT", agg.TTFT)
		printRow("TPOT", agg.TPOT)
		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))
		fmt.Printf("  TPS: %8.2f tok/s  |  TPM: %8.1f tok/min  |  QPS: %.4f req/s  |  QPM: %.2f req/min  |  I/O Ratio: %s\n",
			agg.TPS, agg.TPM, agg.QPS, agg.QPM, formatRatio(agg.IORatio))
	} else {
		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", strings.Repeat("-", colWidth-2))
		fmt.Printf("  QPS: %.4f req/s  |  QPM: %.2f req/min\n", agg.QPS, agg.QPM)
	}

	// 吞吐口径按"窗口内完成"计数，E2E 时延占窗口比例过高时结果偏低，需醒目提示
	if note := throughputBiasNote(agg); note != "" {
		fmt.Printf("  [WARN] %s\n", note)
	}

	// 生成窗口过窄的样本（响应一次性到达）不参与 TPOT/TPS/TPM 分位数
	if agg.GenSpeedExcluded > 0 {
		fmt.Printf("  [NOTE] %d 条样本因生成窗口过窄（响应一次性到达）被剔除出 TPOT/TPS 分位数，疑似网关缓冲或压测机读流饥饿\n",
			agg.GenSpeedExcluded)
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
	fmt.Printf("  %-16s  %-11s  %-11s  %-11s  %-11s  %s\n",
		label,
		formatDuration(s.P50),
		formatDuration(s.P95),
		formatDuration(s.P99),
		formatDuration(s.Avg),
		n,
	)
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
