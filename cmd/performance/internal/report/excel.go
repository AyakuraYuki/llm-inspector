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

// overviewExtra 是总览 sheet 在标准行之后追加的 K/V 行，
// 由 performance-cluster 用来写入节点数量与各节点并发分片等集群信息。
var overviewExtra [][2]string

func SetOverviewExtra(rows [][2]string) { overviewExtra = rows }

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

	// ── Sheet 6: 分位数全景 ───────────────────────────────────────────────
	// 独立成表而不是把 13 个分位数塞进上面每张表：现有 sheet 的列布局保持不变，
	// 需要完整分布时来这张表，一个「档位 × 指标」一行，横向就是完整分位数序列。
	_, _ = f.NewSheet("分位数全景")
	writePercentileSheet(f, results, hdrStyle)

	// ── Sheet 7: 错误分析 ─────────────────────────────────────────────────
	_, _ = f.NewSheet("错误分析")
	writeErrorSheet(f, results, hdrStyle)

	// ── Sheet 8: 错误明细 ─────────────────────────────────────────────────
	_, _ = f.NewSheet("错误明细")
	writeErrorDetailSheet(f, results, hdrStyle)

	// ── Sheet 9: 延迟分布（可选）───────────────────────────────────────────
	// 仅当至少一个档位携带直方图数据（即运行时开启了 show_histogram）才新增，
	// 关闭时 sheet 总数、既有 8 个 sheet 的内容和顺序都不变。
	sheets := []string{
		"TTFT延迟", "生成速度(TPS·token)", "QPS压测(TPS·req)",
		"输入输出Token比", "分位数全景", "错误分析", "错误明细",
	}
	if hasHistogramData(results) {
		_, _ = f.NewSheet("延迟分布")
		writeHistogramSheet(f, results, hdrStyle)
		sheets = append(sheets, "延迟分布")
	}

	// 数据 sheet 统一冻结表头并开启自动筛选：模型数 × 分组 × 并发档位会让行数
	// 迅速膨胀，没有这两项就只能在几百行里靠肉眼定位某个分组的结果。
	for _, sh := range sheets {
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
		{"负载模式", loadModeSummary(cfg)},
		{"请求数制", requestsSummary(cfg)},
		{"输入构造", promptSummary(cfg)},
		{"本地分词器", tokenizerSummary(cfg)},
		{"思考时间", thinkTimeSummary(cfg)},
		{"输出上限", fmt.Sprintf("max_tokens=%d", cfg.EffectiveMaxOutputTokens())},
		{"预热", warmupSummary(cfg)},
		{"模型数量", len(cfg.Models)},
		{"排除模型", excludedModel},
		{"错误率早停", earlyStopSummary(cfg)},
		{"SLO / Goodput", sloConfigSummary(cfg)},
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
	for _, kv := range overviewExtra {
		rows = append(rows, []any{kv[0], kv[1]})
	}
	rows = append(rows, []any{"指标说明", "见下"})
	rows = append(rows, []any{"- TTFT", "首 token 时延"})
	rows = append(rows, []any{"- TPOT", "每 token 生成耗时（gen_window/tokens）"})
	rows = append(rows, []any{"- ITL", "逐输出内容事件之间的间隔（近似逐 token 生成间隔），按 SSE 事件粒度采样：多数 provider 一个事件对应一个或几个 token，并非逐 token 精确值；剔除口径同 TPOT/TPS"})
	rows = append(rows, []any{"- TPS", "per-request tokens/s（P50/P95/P99/P99.5/P99.9；生成窗口 <100ms 或 <5%×E2E 的样本视为一次性到达，不入样，剔除数见备注列）"})
	rows = append(rows, []any{"- TPM", "per-request tokens/min（P50/P95/P99/P99.5/P99.9；入样口径同 TPS）"})
	rows = append(rows, []any{"- Decode tok/s", "单流解码速度（1/平均 TPOT），入样口径同 TPOT/TPS；对标 evalscope 的 Decode tok/s。与 tokens/s Avg 同源但口径不同：后者是各请求速率的算术均值，长短响应混跑时被短响应拉高"})
	rows = append(rows, []any{"- System TPS", "吞吐窗口内完成的总 tokens/窗口时长（窗口外完成的长尾请求不计入）"})
	rows = append(rows, []any{"- QPS（又称RPS）", "系统级 req/s"})
	rows = append(rows, []any{"- QPM（又称RPM）", "系统级 req/min"})
	rows = append(rows, []any{"- I/O Ratio", "输入/输出 token 比（input_tokens/output_tokens，per-request 分位数及 System 总量比）"})
	rows = append(rows, []any{"- Cache Hit Rate", "缓存命中率（cached_input_tokens/input_tokens*100%，input_tokens 为全量输入口径：Anthropic 已补入 cache_read/cache_creation；per-request 分位数及 System 总量比，仅上报了缓存字段的 provider 有效，未上报时显示 N/A）"})
	rows = append(rows, []any{"- Goodput", "满足全部已配置 SLO 阈值（TTFT/TPOT/E2E）的请求占总请求数的比例；未配置 slo 时显示 N/A"})
	rows = append(rows, []any{"- Steady / Last 30s", "稳态吞吐窗口，对标 evalscope 的 Workload Throughput：Steady 掐掉窗口头尾各 10%，只算中间 80% 内完成的请求；Last 30s 只算窗口最后 30s 内完成的请求（窗口不足 30s 为 N/A）。与 Overall（System TPS/QPS）差异大说明档位内负载未进入稳态"})
	rows = append(rows, []any{"- usage 对拍", "配置本地分词器后，对服务端 completion_tokens 与本地对可见文本的计数比较，|偏差| 超过 usage_drift_pct 的样本计为漂移；出现思考内容的请求不对拍（思考 token 不在可见文本里）。大量漂移说明网关/上游 usage 统计与实际输出不符"})
	rows = append(rows, []any{"- 分位数全景 sheet", "每个「档位 × 指标」一行，列出 Min/P10/P25/P50/P75/P90/P95/P99/P99.5/P99.9/Max/Avg/StdDev 全量统计。低分位看整体分布形态，StdDev 看抖动幅度——只看高分位分不出「整体偏慢」与「少数长尾拖高均值」"})

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

// earlyStopSummary 渲染总览 sheet 里的错误率早停配置摘要。
func earlyStopSummary(cfg types.BenchmarkConfig) string {
	if !cfg.EarlyStopEnabled {
		return "未启用"
	}
	skip := "不跳过更高并发档位"
	if cfg.SkipHigherConcurrency {
		skip = "跳过更高并发档位"
	}
	return fmt.Sprintf("启用（阈值 %.1f%%，最少样本 %d，%s）", cfg.MaxErrorRate*100, cfg.MinSamples, skip)
}

// thinkTimeSummary 渲染总览 sheet 里的思考时间摘要。0 要显式说明语义，
// 否则读报告的人无法区分「没配」和「配成了完成即发」。
func thinkTimeSummary(cfg types.BenchmarkConfig) string {
	if cfg.OpenLoop {
		return "不适用（open-loop 的到达节奏由目标 RPS 决定）"
	}
	if cfg.ThinkTime <= 0 {
		return "0s（完成即发，对齐 evalscope --rate -1 语义）"
	}
	return fmt.Sprintf("%s（closed-loop 每个 worker 两次请求之间的等待）", cfg.ThinkTime)
}

// requestsSummary 渲染总览 sheet 里的请求数制摘要。
func requestsSummary(cfg types.BenchmarkConfig) string {
	switch len(cfg.RequestsPerLevel) {
	case 0:
		return fmt.Sprintf("时长制（每档 %s）", cfg.Duration)
	case 1:
		return fmt.Sprintf("每档 %d 个请求与 %s 先到者结束", cfg.RequestsPerLevel[0], cfg.Duration)
	default:
		return fmt.Sprintf("逐档请求数 %v 与 %s 先到者结束", cfg.RequestsPerLevel, cfg.Duration)
	}
}

// promptSummary 渲染总览 sheet 里的输入构造摘要。
func promptSummary(cfg types.BenchmarkConfig) string {
	switch {
	case cfg.DatasetPrompt:
		return fmt.Sprintf("dataset（line_by_line，%d 行，每请求随机取一行）", len(cfg.DatasetLines))
	case cfg.DynamicPrompt:
		length := fmt.Sprintf("约 %d tokens", cfg.PromptTokens)
		if cfg.PromptTokensMin > 0 && cfg.PromptTokensMax > 0 {
			length = fmt.Sprintf("[%d, %d] tokens 均匀采样", cfg.PromptTokensMin, cfg.PromptTokensMax)
		}
		precision := "按字符数近似（≈4 字符/token）"
		if cfg.TokenizerPath != "" {
			precision = "按本地分词器精确收敛"
		}
		return fmt.Sprintf("dynamic（随机长文本 %s，%s）", length, precision)
	case cfg.CodexPrompt:
		return "codex（固定长系统提示词 + 随机简短提问，高相似度请求）"
	default:
		return "text（固定文本）"
	}
}

// tokenizerSummary 渲染总览 sheet 里的本地分词器摘要。
func tokenizerSummary(cfg types.BenchmarkConfig) string {
	if cfg.TokenizerPath == "" {
		return "未配置（输入长度按字符近似，usage 缺失时字符估算，不做 usage 对拍）"
	}
	drift := "usage 对拍关闭"
	if cfg.UsageDriftPct > 0 {
		drift = fmt.Sprintf("usage 对拍阈值 %.1f%%", cfg.UsageDriftPct)
	}
	fp := cfg.TokenizerFingerprint
	if len(fp) > 12 {
		fp = fp[:12]
	}
	return fmt.Sprintf("%s（词表指纹 %s，%s）", cfg.TokenizerPath, fp, drift)
}

// warmupSummary 渲染总览 sheet 里的预热摘要。
func warmupSummary(cfg types.BenchmarkConfig) string {
	if !cfg.Warmup {
		return "关闭"
	}
	if cfg.WarmupPerLevel {
		return fmt.Sprintf("%s，每个并发档位前都预热", cfg.WarmupDuration)
	}
	return fmt.Sprintf("%s，仅每个模型首档前预热", cfg.WarmupDuration)
}

// loadModeSummary 渲染总览 sheet 里的负载模式摘要。
func loadModeSummary(cfg types.BenchmarkConfig) string {
	if !cfg.OpenLoop {
		return "closed-loop（默认）"
	}
	if cfg.OpenLoopUnbounded {
		return fmt.Sprintf("open-loop 无界（目标 RPS：%v，泊松到达，不设在途上限）", cfg.RequestRate)
	}
	return fmt.Sprintf("open-loop（目标 RPS：%v，泊松到达，在途上限 = concurrency）", cfg.RequestRate)
}

// sloConfigSummary 渲染总览 sheet 里的 SLO/Goodput 配置摘要。
func sloConfigSummary(cfg types.BenchmarkConfig) string {
	if !cfg.SLO.Enabled() {
		return "未配置"
	}
	var parts []string
	if cfg.SLO.TTFT > 0 {
		parts = append(parts, fmt.Sprintf("TTFT<=%s", cfg.SLO.TTFT))
	}
	if cfg.SLO.TPOT > 0 {
		parts = append(parts, fmt.Sprintf("TPOT<=%s", cfg.SLO.TPOT))
	}
	if cfg.SLO.E2E > 0 {
		parts = append(parts, fmt.Sprintf("E2E<=%s", cfg.SLO.E2E))
	}
	return strings.Join(parts, ", ")
}

// hasHistogramData 判断本次结果里是否至少有一个档位携带直方图数据。
func hasHistogramData(results []types.AggregatedMetrics) bool {
	for _, agg := range results {
		if len(agg.E2EHistogram) > 0 || len(agg.TTFTHistogram) > 0 {
			return true
		}
	}
	return false
}

// writeHistogramSheet 把每个档位的 E2E/TTFT 延迟分布直方图拆成一行一个分桶。
func writeHistogramSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "延迟分布"
	headers := []any{"模型 ID", "Provider", "Token Group", "并发数", "指标", "区间下限(ms)", "区间上限(ms)", "样本数"}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "D", 10)
	_ = f.SetColWidth(sh, "E", "E", 12)
	_ = f.SetColWidth(sh, "F", "H", 14)

	row := 2
	writeBuckets := func(agg types.AggregatedMetrics, metric string, buckets []types.HistBucket) {
		for _, b := range buckets {
			xlSetRow(f, sh, row, []any{
				agg.Model, string(agg.Provider), agg.TokenGroup, agg.Concurrency, metric,
				durMs(b.Lo), durMs(b.Hi), b.Count,
			}, 0)
			row++
		}
	}
	for _, agg := range results {
		writeBuckets(agg, "E2E Latency", agg.E2EHistogram)
		if agg.Provider != types.ProviderOpenAIImage {
			writeBuckets(agg, "TTFT", agg.TTFTHistogram)
		}
	}
}

// writePercentileSheet 把每个「档位 × 指标」的全量分位数拆成一行。
// 时延类指标统一以 ms 为单位，速率/比例类保留原单位，靠「单位」列区分。
func writePercentileSheet(f *excelize.File, results []types.AggregatedMetrics, hdrStyle int) {
	const sh = "分位数全景"
	headers := []any{
		"模型 ID", "Provider", "Token Group", "并发数", "指标", "单位", "样本数(N)",
		"Min", "P10", "P25", "P50", "P75", "P90", "P95", "P99", "P99.5", "P99.9", "Max", "Avg", "StdDev",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "D", 10)
	_ = f.SetColWidth(sh, "E", "E", 18)
	_ = f.SetColWidth(sh, "F", "G", 10)
	_ = f.SetColWidth(sh, "H", "T", 12)

	row := 2
	emit := func(agg types.AggregatedMetrics, metric, unit string, vals []any, n int) {
		if n == 0 {
			return // 该指标本档无样本，不占行
		}
		head := []any{agg.Model, string(agg.Provider), agg.TokenGroup, agg.Concurrency, metric, unit, n}
		xlSetRow(f, sh, row, append(head, vals...), 0)
		row++
	}
	for _, agg := range results {
		isStreaming := agg.Provider != types.ProviderOpenAIImage
		if isStreaming {
			emit(agg, "TTFT", "ms", durStatCells(agg.TTFT), agg.TTFT.N)
			emit(agg, "TPOT", "ms", durStatCells(agg.TPOT), agg.TPOT.N)
			emit(agg, "ITL", "ms", durStatCells(agg.ITL), agg.ITL.N)
		}
		emit(agg, "E2E Latency", "ms", durStatCells(agg.Latency), agg.Latency.N)
		if isStreaming {
			emit(agg, "tokens/s", "tok/s", floatStatCells(agg.TpsPr), agg.TpsPr.N)
			emit(agg, "TPM", "tok/min", floatStatCells(agg.TpmPr), agg.TpmPr.N)
			emit(agg, "I/O Ratio", "ratio", floatStatCells(agg.IOR), agg.IOR.N)
			emit(agg, "Cache Hit Rate", "%", floatStatCells(agg.CacheHitPr), agg.CacheHitPr.N)
		}
	}
}

// durStatCells 把时延分位数按表头顺序摊平为毫秒数值。这里不用 durMs：
// 0 在本表里是合法取值（单样本的 StdDev、快到测不出的 Min），显示成 N/A 会误导。
func durStatCells(s types.PercentileStats) []any {
	ms := func(d time.Duration) any { return round2(float64(d) / float64(time.Millisecond)) }
	return []any{
		ms(s.Min), ms(s.P10), ms(s.P25), ms(s.P50), ms(s.P75), ms(s.P90),
		ms(s.P95), ms(s.P99), ms(s.P995), ms(s.P999), ms(s.Max), ms(s.Avg), ms(s.StdDev),
	}
}

// floatStatCells 把 float 分位数按表头顺序摊平，同样保留合法的 0。
func floatStatCells(s types.FloatStats) []any {
	return []any{
		round2(s.Min), round2(s.P10), round2(s.P25), round2(s.P50), round2(s.P75), round2(s.P90),
		round2(s.P95), round2(s.P99), round2(s.P995), round2(s.P999), round2(s.Max), round2(s.Avg), round2(s.StdDev),
	}
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
		"ITL P50(ms)", "ITL P95(ms)", "ITL P99(ms)", "ITL P99.5(ms)", "ITL P99.9(ms)", "ITL Avg(ms)", "ITL 样本数(N)",
		"TPM P50", "TPM P95", "TPM P99", "TPM P99.5", "TPM P99.9", "TPM Avg",
		"Decode tok/s",
		"System TPS(tok/s)",
		"System TPM(tok/min)",
		"备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "E", 10)
	secondLastCol := xlCell(len(headers)-1, 1)
	lastCol := xlCell(len(headers), 1)
	_ = f.SetColWidth(sh, "F", secondLastCol[:len(secondLastCol)-1], 14)
	_ = f.SetColWidth(sh, lastCol[:len(lastCol)-1], lastCol[:len(lastCol)-1], 40)

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
			notes = append(notes, fmt.Sprintf("剔除 %d 条未通过速率有效性校验的样本（一次性到达或超出单流物理上限，测不出真实解码速度），ITL 剔除口径同 TPOT/TPS", agg.GenSpeedExcluded))
		}
		if agg.EstimatedOutputs > 0 {
			notes = append(notes, fmt.Sprintf("%d/%d 条成功样本的 token 数为文本估算（无 usage 上报），速率分位数可信度下降", agg.EstimatedOutputs, agg.Success))
		}
		if agg.UsageChecked > 0 {
			notes = append(notes, fmt.Sprintf("usage 对拍 %d 条，%d 条偏差超阈值（|偏差| P50 %.1f%%，Max %.1f%%）", agg.UsageChecked, agg.UsageDrifted, agg.UsageDriftAbs.P50, agg.UsageDriftAbs.Max))
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
			durMs(agg.ITL.P50), durMs(agg.ITL.P95), durMs(agg.ITL.P99), durMs(agg.ITL.P995), durMs(agg.ITL.P999), durMs(agg.ITL.Avg), agg.ITL.N,
			fVal(agg.TpmPr.P50), fVal(agg.TpmPr.P95), fVal(agg.TpmPr.P99), fVal(agg.TpmPr.P995), fVal(agg.TpmPr.P999), fVal(agg.TpmPr.Avg),
			fVal(agg.DecodeTPS),
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
		"模型 ID", "Provider", "Token Group", "类型", "并发数", "目标RPS(open-loop)", "请求数上限", "开始时间",
		"实际时长(s)", "吞吐窗口(s)", "QPS(req/s)", "QPM(req/min)",
		"Steady QPS", "Steady TPS(tok/s)", "Last30s QPS", "Last30s TPS(tok/s)",
		"成功率(%)", "成功请求数", "失败请求数", "Goodput(%)", "SLO 阈值", "备注",
	}
	xlSetRow(f, sh, 1, headers, hdrStyle)
	_ = f.SetColWidth(sh, "A", "A", 30)
	_ = f.SetColWidth(sh, "B", "B", 14)
	_ = f.SetColWidth(sh, "C", "C", 18)
	_ = f.SetColWidth(sh, "D", "D", 14)
	_ = f.SetColWidth(sh, "E", "E", 10)
	_ = f.SetColWidth(sh, "F", "G", 18)
	_ = f.SetColWidth(sh, "H", "H", 22)
	_ = f.SetColWidth(sh, "I", "T", 12)
	_ = f.SetColWidth(sh, "U", "U", 30)
	_ = f.SetColWidth(sh, "V", "V", 55)

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
		note := throughputBiasNote(agg)
		if agg.StoppedEarly {
			if note != "" {
				note += "；"
			}
			note += "本档位因错误率超阈值被提前终止"
		}
		targetRate := "N/A"
		if agg.TargetRate > 0 {
			targetRate = fmt.Sprintf("%.2f", agg.TargetRate)
		}
		goodput, sloText := "N/A", "N/A"
		if agg.SLOConfigured {
			goodput = fmt.Sprintf("%.1f", agg.GoodputRatio)
			sloText = sloSummary(agg, agg.Provider != types.ProviderOpenAIImage)
		}
		reqCap := "N/A"
		if agg.RequestLimit > 0 {
			reqCap = fmt.Sprintf("%d", agg.RequestLimit)
		}
		// 稳态/Last30s：没有时间轴（SteadyWindow 为 0）或窗口不足 30s 时为 N/A；
		// 图片端点没有 token，TPS 列恒为 N/A
		var steadyQPS, steadyTPS, last30QPS, last30TPS any = "N/A", "N/A", "N/A", "N/A"
		if agg.SteadyWindow > 0 {
			steadyQPS = round3(agg.SteadyQPS)
			if category == "text" {
				steadyTPS = round2(agg.SteadyTPS)
			}
		}
		if agg.Last30sWindow > 0 {
			last30QPS = round3(agg.Last30sQPS)
			if category == "text" {
				last30TPS = round2(agg.Last30sTPS)
			}
		}
		xlSetRow(f, sh, row, []any{
			agg.Model,
			string(agg.Provider),
			agg.TokenGroup,
			category,
			agg.Concurrency,
			targetRate,
			reqCap,
			startStr,
			round2(agg.Elapsed.Seconds()),
			round2(agg.Window.Seconds()),
			round3(agg.QPS),
			round2(agg.QPM),
			steadyQPS,
			steadyTPS,
			last30QPS,
			last30TPS,
			round1(successPct),
			agg.Success,
			agg.Failed,
			goodput,
			sloText,
			note,
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
	headers := []any{"模型 ID", "Provider", "Token Group", "并发数", "总请求数", "失败总数", "成功率(%)", "是否提前终止"}
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
			yesNo(agg.StoppedEarly),
		}
		for _, et := range types.ErrorTypeOrder {
			values = append(values, agg.ErrorCounts[et])
		}
		xlSetRow(f, sh, row, values, 0)
		row++
	}
}

// yesNo 把布尔值渲染成中文"是"/"否"。
func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
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
