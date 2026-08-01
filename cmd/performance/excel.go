package main

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"
)

// exportExcel 将基准测试结果导出为 xlsx 文件，结构仿照 build_metrics_report.py。
func exportExcel(cfg BenchmarkConfig, results []AggregatedMetrics, runAt time.Time, outPath string) error {
	f := excelize.NewFile()
	defer func(f *excelize.File) { _ = f.Close() }(f)

	// ── 样式 ──────────────────────────────────────────────────────────────
	titleStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})
	hdrStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"D9E1F2"}, Pattern: 1},
	})
	labelStyle, _ := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})

	// ── Sheet 1: 总览 ─────────────────────────────────────────────────────
	_ = f.SetSheetName("Sheet1", "总览")
	writeOverview(f, cfg, results, runAt, titleStyle, labelStyle)

	// ── Sheet 2: TTFT延迟 ─────────────────────────────────────────────────
	_, _ = f.NewSheet("TTFT延迟")
	writeTTFTSheet(f, results, hdrStyle)

	// ── Sheet 3: 生成速度(TPS·token) ──────────────────────────────────────
	_, _ = f.NewSheet("生成速度(TPS·token)")
	writeGenSheet(f, results, hdrStyle)

	// ── Sheet 4: QPS压测(TPS·req) ─────────────────────────────────────────
	_, _ = f.NewSheet("QPS压测(TPS·req)")
	writeQPSSheet(f, results, hdrStyle)

	// ── Sheet 5: 输入输出Token比 ─────────────────────────────────────────
	_, _ = f.NewSheet("输入输出Token比")
	writeIORSheet(f, results, hdrStyle)

	// ── Sheet 6: 错误分析 ─────────────────────────────────────────────────
	_, _ = f.NewSheet("错误分析")
	writeErrorSheet(f, results, hdrStyle)

	// ── Sheet 7: 错误明细 ─────────────────────────────────────────────────
	_, _ = f.NewSheet("错误明细")
	writeErrorDetailSheet(f, results, hdrStyle)

	return f.SaveAs(outPath)
}

// ── Sheet writers ─────────────────────────────────────────────────────────────

func writeOverview(f *excelize.File, cfg BenchmarkConfig, results []AggregatedMetrics,
	runAt time.Time, titleStyle, labelStyle int) {

	const sh = "总览"
	xlSetCell(f, sh, 1, 1, "LLM性能基准测试报告")
	_ = f.SetCellStyle(sh, "A1", "A1", titleStyle)
	_ = f.SetColWidth(sh, "A", "A", 24)
	_ = f.SetColWidth(sh, "B", "B", 55)

	rows := [][]any{
		{"测试日期", runAt.Format("2006-01-02 15:04:05")},
		{"接口地址", cfg.BaseURL},
		{"时长 / 并发档位", cfg.Duration.String()},
		{"并发档位", fmt.Sprintf("%v", cfg.Concurrency)},
		{"模型数量", len(cfg.Models)},
		{"排除模型", excludedModel},
	}
	for _, m := range cfg.Models {
		rows = append(rows, []any{
			fmt.Sprintf("  模型 [%s]", m.Provider),
			m.Name,
		})
	}
	rows = append(rows, []any{"指标说明", "见下"})
	rows = append(rows, []any{"- TTFT", "首 token 时延"})
	rows = append(rows, []any{"- TPOT", "每 token 生成耗时（gen_window/tokens）"})
	rows = append(rows, []any{"- TPS", "per-request tokens/s（P50/P95/P99/P99.5/P99.9）"})
	rows = append(rows, []any{"- TPM", "per-request tokens/min（P50/P95/P99/P99.5/P99.9）"})
	rows = append(rows, []any{"- System TPS", "总 tokens/elapsed"})
	rows = append(rows, []any{"- QPS（又称RPS）", "系统级 req/s"})
	rows = append(rows, []any{"- QPM（又称RPM）", "系统级 req/min"})
	rows = append(rows, []any{"- I/O Ratio", "输出/输入 token 比（output_tokens/input_tokens，per-request 分位数及 System 总量比）"})
	rows = append(rows, []any{"- Cache Hit Rate", "缓存命中率（cached_input_tokens/input_tokens*100%，per-request 分位数及 System 总量比，仅部分 provider 上报缓存字段，未上报时显示 N/A）"})

	for i, row := range rows {
		xlSetCell(f, sh, 1, i+3, row[0])
		xlSetCell(f, sh, 2, i+3, row[1])
		_ = f.SetCellStyle(sh, xlCell(1, i+3), xlCell(1, i+3), labelStyle)
	}
}

func writeTTFTSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "TTFT延迟"
	headers := []any{
		"模型 ID", "Provider", "并发数", "样本数(N)",
		"TTFT P50(ms)", "TTFT P95(ms)", "TTFT P99(ms)", "TTFT P99.5(ms)", "TTFT P99.9(ms)", "TTFT Avg(ms)",
		"E2E P50(ms)", "E2E P95(ms)", "E2E P99(ms)", "E2E P99.5(ms)", "E2E P99.9(ms)", "E2E Avg(ms)",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "D", 10)
	_ = f.SetColWidth(sh, "E", "L", 14)

	row := 2
	for _, agg := range results {
		if agg.Provider == ProviderOpenAIImage {
			continue
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.Concurrency,
			agg.TTFT.N,
			durMs(agg.TTFT.P50), durMs(agg.TTFT.P95), durMs(agg.TTFT.P99), durMs(agg.TTFT.P995), durMs(agg.TTFT.P999), durMs(agg.TTFT.Avg),
			durMs(agg.Latency.P50), durMs(agg.Latency.P95), durMs(agg.Latency.P99), durMs(agg.Latency.P995), durMs(agg.Latency.P999), durMs(agg.Latency.Avg),
		}, 0)
		row++
	}
}

func writeGenSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "生成速度(TPS·token)"
	headers := []any{
		"模型 ID",
		"Provider",
		"并发数",
		"样本数(N)",
		"tokens/s P50", "tokens/s P95", "tokens/s P99", "tokens/s P99.5", "tokens/s P99.9", "tokens/s Avg",
		"TPOT P50(ms)", "TPOT P95(ms)", "TPOT P99(ms)", "TPOT P99.5(ms)", "TPOT P99.9(ms)", "TPOT Avg(ms)",
		"TPM P50", "TPM P95", "TPM P99", "TPM P99.5", "TPM P99.9", "TPM Avg",
		"System TPS(tok/s)",
		"System TPM(tok/min)",
		"备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "D", 10)
	_ = f.SetColWidth(sh, "E", "R", 14)
	_ = f.SetColWidth(sh, "S", "S", 40)

	row := 2
	for _, agg := range results {
		if agg.Provider == ProviderOpenAIImage {
			continue
		}
		note := ""
		if agg.TpsPr.N > 0 && agg.TpsPr.N < 20 {
			note = fmt.Sprintf("样本量少(N=%d)，P99 仅供参考", agg.TpsPr.N)
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.Concurrency,
			agg.TpsPr.N,
			fVal(agg.TpsPr.P50), fVal(agg.TpsPr.P95), fVal(agg.TpsPr.P99), fVal(agg.TpsPr.P995), fVal(agg.TpsPr.P999), fVal(agg.TpsPr.Avg),
			durMs(agg.TPOT.P50), durMs(agg.TPOT.P95), durMs(agg.TPOT.P99), durMs(agg.TPOT.P995), durMs(agg.TPOT.P999), durMs(agg.TPOT.Avg),
			fVal(agg.TpmPr.P50), fVal(agg.TpmPr.P95), fVal(agg.TpmPr.P99), fVal(agg.TpmPr.P995), fVal(agg.TpmPr.P999), fVal(agg.TpmPr.Avg),
			fVal(agg.TPS),
			fVal(agg.TPM),
			note,
		}, 0)
		row++
	}
}

func writeIORSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "输入输出Token比"
	headers := []any{
		"模型 ID", "Provider", "并发数", "样本数(N)",
		"I/O Ratio P50", "I/O Ratio P95", "I/O Ratio P99", "I/O Ratio P99.5", "I/O Ratio P99.9", "I/O Ratio Avg",
		"System I/O Ratio",
		"Cache Hit Rate P50(%)", "Cache Hit Rate P95(%)", "Cache Hit Rate P99(%)", "Cache Hit Rate P99.5(%)", "Cache Hit Rate P99.9(%)", "Cache Hit Rate Avg(%)",
		"System Cache Hit Rate(%)", "总输入Token数", "总缓存命中Token数",
		"备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "D", 10)
	_ = f.SetColWidth(sh, "E", "K", 16)
	_ = f.SetColWidth(sh, "L", "R", 18)
	_ = f.SetColWidth(sh, "S", "T", 14)
	_ = f.SetColWidth(sh, "U", "U", 40)

	row := 2
	for _, agg := range results {
		if agg.Provider == ProviderOpenAIImage {
			continue
		}
		note := ""
		if agg.IOR.N > 0 && agg.IOR.N < 20 {
			note = fmt.Sprintf("样本量少(N=%d)，P99 仅供参考", agg.IOR.N)
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.Concurrency,
			agg.IOR.N,
			fVal(agg.IOR.P50), fVal(agg.IOR.P95), fVal(agg.IOR.P99), fVal(agg.IOR.P995), fVal(agg.IOR.P999), fVal(agg.IOR.Avg),
			fVal(agg.IORatio),
			fVal(agg.CacheHitPr.P50), fVal(agg.CacheHitPr.P95), fVal(agg.CacheHitPr.P99), fVal(agg.CacheHitPr.P995), fVal(agg.CacheHitPr.P999), fVal(agg.CacheHitPr.Avg),
			fVal(agg.CacheHitRatio), agg.TotalInputTokens, agg.TotalCachedTokens,
			note,
		}, 0)
		row++
	}
}

func writeQPSSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "QPS压测(TPS·req)"
	headers := []any{
		"模型 ID", "Provider", "类型", "并发数",
		"持续时长(s)", "QPS(req/s)", "QPM(req/min)", "成功率(%)",
		"成功请求数", "失败请求数",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "C", 14)
	_ = f.SetColWidth(sh, "D", "J", 12)

	row := 2
	for _, agg := range results {
		category := "text"
		if agg.Provider == ProviderOpenAIImage {
			category = "image"
		}
		successPct := 0.0
		if agg.Total > 0 {
			successPct = float64(agg.Success) / float64(agg.Total) * 100
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			category,
			agg.Concurrency,
			round2(agg.Elapsed.Seconds()),
			round3(agg.QPS),
			round2(agg.QPM),
			round1(successPct),
			agg.Success,
			agg.Failed,
		}, 0)
		row++
	}
}

// ── Cell helpers ──────────────────────────────────────────────────────────────

// xlSetRow 在 sheet 的 rowIdx 行写入 values；若 style>0 则同时设置样式。
func xlSetRow(f *excelize.File, sheet string, rowIdx int, values []any, style int) {
	for col, v := range values {
		cell := xlCell(col+1, rowIdx)
		_ = f.SetCellValue(sheet, cell, v)
		if style > 0 {
			_ = f.SetCellStyle(sheet, cell, cell, style)
		}
	}
}

// xlSetCell 设置单个单元格的值（col/row 均为 1-indexed）。
func xlSetCell(f *excelize.File, sheet string, col, row int, value any) {
	_ = f.SetCellValue(sheet, xlCell(col, row), value)
}

// xlCell 将 (col, row) 转为 "A1" 格式的单元格名称。
func xlCell(col, row int) string {
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

// durMs 将 time.Duration 转为毫秒（保留 2 位小数），≤0 返回 "N/A"。
func durMs(d time.Duration) any {
	if d <= 0 {
		return "N/A"
	}
	return math.Round(float64(d.Nanoseconds())/1e4) / 100
}

// fVal 将 float64 保留 2 位小数，≤0 返回 "N/A"。
func fVal(v float64) any {
	if v <= 0 {
		return "N/A"
	}
	return round2(v)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func writeErrorSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "错误分析"
	headers := []any{"模型 ID", "Provider", "并发数", "总请求数", "失败总数", "成功率(%)"}
	for _, et := range errorTypeOrder {
		headers = append(headers, string(et))
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	lastCol := xlCell(len(headers), 1)
	_ = f.SetColWidth(sh, "C", lastCol[:len(lastCol)-1], 14)

	row := 2
	for _, agg := range results {
		successPct := 0.0
		if agg.Total > 0 {
			successPct = float64(agg.Success) / float64(agg.Total) * 100
		}
		values := []any{
			agg.Model,
			string(agg.Provider),
			agg.Concurrency,
			agg.Total,
			agg.Failed,
			round1(successPct),
		}
		for _, et := range errorTypeOrder {
			values = append(values, agg.ErrorCounts[et])
		}
		xlSetRow(f, sh, row, values, 0)
		row++
	}
}

// writeErrorDetailSheet 把每条失败请求的原始记录按发生时间排成一行一条的错误日志。
func writeErrorDetailSheet(f *excelize.File, results []AggregatedMetrics, hdrStyle int) {
	const sh = "错误明细"
	headers := []any{"序号", "模型 ID", "Provider", "并发数", "发生时间", "错误类型", "总时延(ms)", "错误信息"}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 8)
	_ = f.SetColWidth(sh, "B", "B", 30)
	_ = f.SetColWidth(sh, "C", "C", 16)
	_ = f.SetColWidth(sh, "D", "D", 10)
	_ = f.SetColWidth(sh, "E", "E", 22)
	_ = f.SetColWidth(sh, "F", "F", 16)
	_ = f.SetColWidth(sh, "G", "G", 12)
	_ = f.SetColWidth(sh, "H", "H", 90)

	type detailRow struct {
		model       string
		provider    Provider
		concurrency int
		m           RequestMetrics
	}
	var rows []detailRow
	for _, agg := range results {
		for _, m := range agg.FailedDetails {
			rows = append(rows, detailRow{agg.Model, agg.Provider, agg.Concurrency, m})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].m.Timestamp.Before(rows[j].m.Timestamp) })

	row := 2
	for i, r := range rows {
		xlSetRow(f, sh, row, []any{
			i + 1,
			r.model,
			string(r.provider),
			r.concurrency,
			r.m.Timestamp.Format("2006-01-02 15:04:05.000"),
			string(r.m.ErrorType),
			durMs(r.m.TotalLatency),
			r.m.Error,
		}, 0)
		row++
	}
}
