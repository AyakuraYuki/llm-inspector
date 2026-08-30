package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

var excludedModel = ""

func SetExcludedModel(model string) { excludedModel = model }

// ExportExcel 将基准测试结果导出为 xlsx 文件，结构仿照 build_metrics_report.py。
func ExportExcel(cfg types.BenchmarkConfig, results []types.AggregatedMetrics, runAt time.Time, outPath string) error {
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
	writeOverview(f, cfg, runAt, titleStyle, labelStyle)

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

	// 数据 sheet 统一冻结表头并开启自动筛选：模型数 × 分组 × 并发档位会让行数
	// 迅速膨胀，没有这两项就只能在几百行里靠肉眼定位某个分组的结果。
	for _, sh := range []string{
		"TTFT延迟", "生成速度(TPS·token)", "QPS压测(TPS·req)",
		"输入输出Token比", "错误分析", "错误明细",
	} {
		freezeHeaderWithFilter(f, sh)
	}

	return f.SaveAs(outPath)
}

// freezeHeaderWithFilter 冻结 sheet 的首行表头，并对表头范围开启自动筛选。
func freezeHeaderWithFilter(f *excelize.File, sheet string) {
	_ = f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		Split:       false,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	})

	cols, err := f.GetCols(sheet)
	if err != nil || len(cols) == 0 {
		return
	}
	lastCol, err := excelize.ColumnNumberToName(len(cols))
	if err != nil {
		return
	}
	_ = f.AutoFilter(sheet, fmt.Sprintf("A1:%s1", lastCol), nil)
}

// ── Sheet writers ─────────────────────────────────────────────────────────────

func writeOverview(f *excelize.File, cfg types.BenchmarkConfig, runAt time.Time, titleStyle, labelStyle int) {
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
			fmt.Sprintf("%s（Token Group: %s，%d 个 key）", m.Name, m.TokenGroup, len(m.Tokens)),
		})
	}

	// Token 分组概览：每个分组的 key 数量和挂在它下面的模型，
	// 用于核对「哪个模型走了哪条渠道」。只统计数量，不写出 key 本身。
	groups := groupOrder(cfg)
	rows = append(rows, []any{"Token 分组", fmt.Sprintf("%d 个分组", len(groups))})
	for _, g := range groups {
		rows = append(rows, []any{
			fmt.Sprintf("  分组 %s", g.name),
			fmt.Sprintf("%d 个 key，模型：%s", g.keys, strings.Join(g.models, "、")),
		})
	}
	rows = append(rows, []any{"指标说明", "见下"})
	rows = append(rows, []any{"- TTFT", "首 token 时延"})
	rows = append(rows, []any{"- TPOT", "每 token 生成耗时（gen_window/tokens）"})
	rows = append(rows, []any{"- TPS", "per-request tokens/s（P50/P95/P99/P99.5/P99.9；生成窗口 <100ms 或 <5%×E2E 的样本视为一次性到达，不入样，剔除数见备注列）"})
	rows = append(rows, []any{"- TPM", "per-request tokens/min（P50/P95/P99/P99.5/P99.9；入样口径同 TPS）"})
	rows = append(rows, []any{"- System TPS", "吞吐窗口内完成的总 tokens/窗口时长（窗口外完成的长尾请求不计入）"})
	rows = append(rows, []any{"- QPS（又称RPS）", "系统级 req/s"})
	rows = append(rows, []any{"- QPM（又称RPM）", "系统级 req/min"})
	rows = append(rows, []any{"- I/O Ratio", "输出/输入 token 比（output_tokens/input_tokens，per-request 分位数及 System 总量比）"})
	rows = append(rows, []any{"- Cache Hit Rate", "缓存命中率（cached_input_tokens/input_tokens*100%，input_tokens 为全量输入口径：Anthropic 已补入 cache_read/cache_creation；per-request 分位数及 System 总量比，仅上报了缓存字段的 provider 有效，未上报时显示 N/A）"})

	for i, row := range rows {
		xlSetCell(f, sh, 1, i+3, row[0])
		xlSetCell(f, sh, 2, i+3, row[1])
		_ = f.SetCellStyle(sh, xlCell(1, i+3), xlCell(1, i+3), labelStyle)
	}
}

// tokenGroupSummary 汇总一个 token 分组的 key 数量和使用它的模型。
type tokenGroupSummary struct {
	name   string
	keys   int
	models []string
}

// groupOrder 按分组在 models 中首次出现的顺序汇总各分组，保证报表顺序稳定。
func groupOrder(cfg types.BenchmarkConfig) []tokenGroupSummary {
	var order []string
	byName := make(map[string]*tokenGroupSummary)
	for _, m := range cfg.Models {
		g, ok := byName[m.TokenGroup]
		if !ok {
			g = &tokenGroupSummary{name: m.TokenGroup, keys: len(m.Tokens)}
			byName[m.TokenGroup] = g
			order = append(order, m.TokenGroup)
		}
		g.models = append(g.models, m.Name)
	}

	summaries := make([]tokenGroupSummary, 0, len(order))
	for _, name := range order {
		summaries = append(summaries, *byName[name])
	}
	return summaries
}

func writeTTFTSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "TTFT延迟"
	headers := []any{
		"模型 ID", "Provider", "Token Group", "并发数", "样本数(N)",
		"TTFT P50(ms)", "TTFT P95(ms)", "TTFT P99(ms)", "TTFT P99.5(ms)", "TTFT P99.9(ms)", "TTFT Avg(ms)",
		"E2E P50(ms)", "E2E P95(ms)", "E2E P99(ms)", "E2E P99.5(ms)", "E2E P99.9(ms)", "E2E Avg(ms)",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "E", 10)
	_ = f.SetColWidth(sh, "F", "Q", 14)

	row := 2
	for _, agg := range results {
		if agg.Provider == types.ProviderOpenAIImage {
			continue
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
			agg.Concurrency,
			agg.TTFT.N,
			durMs(agg.TTFT.P50), durMs(agg.TTFT.P95), durMs(agg.TTFT.P99), durMs(agg.TTFT.P995), durMs(agg.TTFT.P999), durMs(agg.TTFT.Avg),
			durMs(agg.Latency.P50), durMs(agg.Latency.P95), durMs(agg.Latency.P99), durMs(agg.Latency.P995), durMs(agg.Latency.P999), durMs(agg.Latency.Avg),
		}, 0)
		row++
	}
}

func writeGenSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "生成速度(TPS·token)"
	headers := []any{
		"模型 ID",
		"Provider",
		"Token Group",
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
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "E", 10)
	_ = f.SetColWidth(sh, "F", "S", 14)
	_ = f.SetColWidth(sh, "T", "T", 40)

	row := 2
	for _, agg := range results {
		if agg.Provider == types.ProviderOpenAIImage {
			continue
		}
		var notes []string
		if agg.TpsPr.N > 0 && agg.TpsPr.N < 20 {
			notes = append(notes, fmt.Sprintf("样本量少(N=%d)，P99 仅供参考", agg.TpsPr.N))
		}
		if agg.GenSpeedExcluded > 0 {
			notes = append(notes, fmt.Sprintf("剔除 %d 条未通过速率有效性校验的样本（一次性到达或超出单流物理上限，测不出真实解码速度）", agg.GenSpeedExcluded))
		}
		if agg.EstimatedOutputs > 0 {
			notes = append(notes, fmt.Sprintf("%d/%d 条成功样本的 token 数为文本估算（无 usage 上报），速率分位数可信度下降", agg.EstimatedOutputs, agg.Success))
		}
		note := strings.Join(notes, "；")
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
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

func writeIORSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "输入输出Token比"
	headers := []any{
		"模型 ID", "Provider", "Token Group", "并发数", "样本数(N)",
		"I/O Ratio P50", "I/O Ratio P95", "I/O Ratio P99", "I/O Ratio P99.5", "I/O Ratio P99.9", "I/O Ratio Avg",
		"System I/O Ratio",
		"Cache Hit Rate P50(%)", "Cache Hit Rate P95(%)", "Cache Hit Rate P99(%)", "Cache Hit Rate P99.5(%)", "Cache Hit Rate P99.9(%)", "Cache Hit Rate Avg(%)",
		"System Cache Hit Rate(%)", "总输入Token数", "总缓存命中Token数",
		"备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "E", 10)
	_ = f.SetColWidth(sh, "F", "L", 16)
	_ = f.SetColWidth(sh, "M", "S", 18)
	_ = f.SetColWidth(sh, "T", "U", 14)
	_ = f.SetColWidth(sh, "V", "V", 40)

	row := 2
	for _, agg := range results {
		if agg.Provider == types.ProviderOpenAIImage {
			continue
		}
		note := ""
		if agg.IOR.N > 0 && agg.IOR.N < 20 {
			note = fmt.Sprintf("样本量少(N=%d)，P99 仅供参考", agg.IOR.N)
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
			agg.Concurrency,
			agg.IOR.N,
			fVal(agg.IOR.P50), fVal(agg.IOR.P95), fVal(agg.IOR.P99), fVal(agg.IOR.P995), fVal(agg.IOR.P999), fVal(agg.IOR.Avg),
			fVal(agg.IORatio),
			fCache(agg.CacheHitPr.P50, agg.CacheHitPr.N), fCache(agg.CacheHitPr.P95, agg.CacheHitPr.N), fCache(agg.CacheHitPr.P99, agg.CacheHitPr.N), fCache(agg.CacheHitPr.P995, agg.CacheHitPr.N), fCache(agg.CacheHitPr.P999, agg.CacheHitPr.N), fCache(agg.CacheHitPr.Avg, agg.CacheHitPr.N),
			fCache(agg.CacheHitRatio, agg.CacheReportedCount), agg.TotalInputTokens, agg.TotalCachedTokens,
			note,
		}, 0)
		row++
	}
}

func writeQPSSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "QPS压测(TPS·req)"
	headers := []any{
		"模型 ID", "Provider", "Token Group", "类型", "并发数", "开始时间",
		"实际时长(s)", "吞吐窗口(s)", "QPS(req/s)", "QPM(req/min)", "成功率(%)",
		"成功请求数", "失败请求数", "备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "D", 14)
	_ = f.SetColWidth(sh, "E", "E", 10)
	_ = f.SetColWidth(sh, "F", "F", 22)
	_ = f.SetColWidth(sh, "G", "M", 12)
	_ = f.SetColWidth(sh, "N", "N", 55)

	row := 2
	for _, agg := range results {
		category := "text"
		if agg.Provider == types.ProviderOpenAIImage {
			category = "image"
		}
		successPct := 0.0
		if agg.Total > 0 {
			successPct = float64(agg.Success) / float64(agg.Total) * 100
		}
		startStr := ""
		if !agg.Start.IsZero() {
			startStr = agg.Start.Format("2006-01-02 15:04:05")
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
			category,
			agg.Concurrency,
			startStr,
			round2(agg.Elapsed.Seconds()),
			round2(agg.Window.Seconds()),
			round3(agg.QPS),
			round2(agg.QPM),
			round1(successPct),
			agg.Success,
			agg.Failed,
			throughputBiasNote(agg),
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

// fCache 渲染缓存命中率：provider 未上报缓存字段（n == 0）时返回 "N/A"，
// 否则保留 2 位小数——0.00% 是合法命中率（缓存启用但未命中），与 N/A 区分。
func fCache(v float64, n int) any {
	if n == 0 {
		return "N/A"
	}
	return round2(v)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func writeErrorSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "错误分析"
	headers := []any{"模型 ID", "Provider", "Token Group", "并发数", "总请求数", "失败总数", "成功率(%)"}
	for _, et := range types.ErrorTypeOrder {
		headers = append(headers, string(et))
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	lastCol := xlCell(len(headers), 1)
	_ = f.SetColWidth(sh, "D", lastCol[:len(lastCol)-1], 14)

	row := 2
	for _, agg := range results {
		successPct := 0.0
		if agg.Total > 0 {
			successPct = float64(agg.Success) / float64(agg.Total) * 100
		}
		values := []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
			agg.Concurrency,
			agg.Total,
			agg.Failed,
			round1(successPct),
		}
		for _, et := range types.ErrorTypeOrder {
			values = append(values, agg.ErrorCounts[et])
		}
		xlSetRow(f, sh, row, values, 0)
		row++
	}
}

// writeErrorDetailSheet 把每条失败请求的原始记录按发生时间排成一行一条的错误日志。
func writeErrorDetailSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "错误明细"
	headers := []any{"序号", "模型 ID", "Provider", "Token Group", "并发数", "发生时间", "错误类型", "RequestID", "总时延(ms)", "错误信息"}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 8)
	_ = f.SetColWidth(sh, "B", "B", 30)
	_ = f.SetColWidth(sh, "C", "C", 16)
	_ = f.SetColWidth(sh, "D", "D", 18)
	_ = f.SetColWidth(sh, "E", "E", 10)
	_ = f.SetColWidth(sh, "F", "F", 22)
	_ = f.SetColWidth(sh, "G", "G", 16)
	_ = f.SetColWidth(sh, "H", "H", 36)
	_ = f.SetColWidth(sh, "I", "I", 12)
	_ = f.SetColWidth(sh, "J", "J", 90)

	type detailRow struct {
		model       string
		provider    types.Provider
		tokenGroup  string
		concurrency int
		m           types.RequestMetrics
	}
	var rows []detailRow
	for _, agg := range results {
		for _, m := range agg.FailedDetails {
			rows = append(rows, detailRow{agg.Model, agg.Provider, agg.TokenGroup, agg.Concurrency, m})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].m.Timestamp.Before(rows[j].m.Timestamp) })

	row := 2
	for i, r := range rows {
		xlSetRow(f, sh, row, []any{
			i + 1,
			r.model,
			string(r.provider),
			r.tokenGroup,
			r.concurrency,
			r.m.Timestamp.Format("2006-01-02 15:04:05.000"),
			string(r.m.ErrorType),
			r.m.RequestID,
			durMs(r.m.TotalLatency),
			r.m.Error,
		}, 0)
		row++
	}
}
