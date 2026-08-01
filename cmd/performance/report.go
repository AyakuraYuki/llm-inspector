package main

import (
	"fmt"
	"strings"
	"time"
)

const colWidth = 80

// printReport 打印所有聚合结果的详细报告和末尾汇总表。
func printReport(results []AggregatedMetrics) {
	fmt.Printf("\n%s\n", rep("=", colWidth))
	fmt.Printf("  BENCHMARK RESULTS\n")
	fmt.Printf("%s\n", rep("=", colWidth))

	for _, agg := range results {
		printOne(agg)
	}

	// 末尾 summary 表
	printSummaryTable(results)
}

func printOne(agg AggregatedMetrics) {
	isStreaming := agg.Provider != ProviderOpenAIImage
	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}

	fmt.Printf("\n%s\n", rep("-", colWidth))
	fmt.Printf("  Model: %s  |  Provider: %s  |  Concurrency: %d\n",
		agg.Model, agg.Provider, agg.Concurrency)
	fmt.Printf("  Elapsed: %s  |  Requests: %d total, %d ok, %d failed (%.1f%% error)\n",
		fmtDur(agg.Elapsed), agg.Total, agg.Success, agg.Failed, errPct)
	fmt.Printf("%s\n", rep("-", colWidth))

	if isStreaming {
		hdr := fmt.Sprintf("  %-16s  %-11s  %-11s  %-11s  %-11s  %s",
			"Metric", "P50", "P95", "P99", "Avg", "N")
		fmt.Println(hdr)
		fmt.Printf("  %s\n", rep("-", colWidth-2))

		printRow("TTFT", agg.TTFT)
		printRow("TPOT", agg.TPOT)
		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", rep("-", colWidth-2))
		fmt.Printf("  TPS: %8.2f tok/s  |  TPM: %8.1f tok/min  |  QPS: %.4f req/s  |  QPM: %.2f req/min  |  I/O Ratio: %s\n",
			agg.TPS, agg.TPM, agg.QPS, agg.QPM, fmtRatio(agg.IORatio))
	} else {
		hdr := fmt.Sprintf("  %-16s  %-11s  %-11s  %-11s  %-11s  %s",
			"Metric", "P50", "P95", "P99", "Avg", "N")
		fmt.Println(hdr)
		fmt.Printf("  %s\n", rep("-", colWidth-2))

		printRow("E2E Latency", agg.Latency)

		fmt.Printf("  %s\n", rep("-", colWidth-2))
		fmt.Printf("  QPS: %.4f req/s  |  QPM: %.2f req/min\n", agg.QPS, agg.QPM)
	}

	// 失败原因分类
	if agg.Failed > 0 && len(agg.ErrorCounts) > 0 {
		var parts []string
		for _, et := range errorTypeOrder {
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

func printRow(label string, s PercentileStats) {
	n := "-"
	if s.N > 0 {
		n = fmt.Sprintf("%d", s.N)
	}
	fmt.Printf("  %-16s  %-11s  %-11s  %-11s  %-11s  %s\n",
		label,
		fmtDur(s.P50),
		fmtDur(s.P95),
		fmtDur(s.P99),
		fmtDur(s.Avg),
		n,
	)
}

func printSummaryTable(results []AggregatedMetrics) {
	fmt.Printf("\n%s\n", rep("=", colWidth))
	fmt.Println("  SUMMARY TABLE")
	fmt.Printf("%s\n", rep("-", colWidth))
	fmt.Printf("  %-26s  %-5s  %-8s  %-8s  %-10s  %-10s  %-10s\n",
		"Model (Provider)", "Conc", "QPS", "TPS", "TTFT P50", "TTFT P95", "I/O Ratio")
	fmt.Printf("  %s\n", rep("-", colWidth-2))

	for _, agg := range results {
		label := fmt.Sprintf("%s (%s)", agg.Model, agg.Provider)
		if len(label) > 26 {
			label = label[:23] + "..."
		}
		tpsStr := "N/A"
		if agg.TPS > 0 {
			tpsStr = fmt.Sprintf("%.1f", agg.TPS)
		}
		fmt.Printf("  %-26s  %-5d  %-8.3f  %-8s  %-10s  %-10s  %-10s\n",
			label,
			agg.Concurrency,
			agg.QPS,
			tpsStr,
			fmtDur(agg.TTFT.P50),
			fmtDur(agg.TTFT.P95),
			fmtRatio(agg.IORatio),
		)
	}

	fmt.Printf("%s\n\n", rep("=", colWidth))
}

// fmtDur 格式化时延为可读字符串。
func fmtDur(d time.Duration) string {
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

func rep(s string, n int) string {
	return strings.Repeat(s, n)
}

// fmtRatio 格式化输入/输出 token 比，无数据时返回 "N/A"。
func fmtRatio(r float64) string {
	if r <= 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.3f", r)
}
