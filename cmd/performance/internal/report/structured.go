package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// jsonReport 是 JSON 结构化报告的顶层结构。刻意不直接 json.Marshal
// types.AggregatedMetrics：那样会把 FailedDetails（每条失败请求的完整
// 错误记录）整条序出去，体积随失败数线性增长，且不是 CI 基线对比真正
// 需要的信息；这里只保留分位数/吞吐/goodput/错误计数摘要。
type jsonReport struct {
	RunAt  time.Time         `json:"run_at"`
	Config jsonConfigSummary `json:"config"`
	Levels []jsonLevel       `json:"levels"`
}

type jsonConfigSummary struct {
	BaseURL     string    `json:"base_url"`
	Duration    string    `json:"duration"`
	Concurrency []int     `json:"concurrency"`
	LoadMode    string    `json:"load_mode"`
	RequestRate []float64 `json:"request_rate,omitzero"`

	// ThinkTime/MaxOutputTokens 直接影响吞吐与时延数字，必须随报告留档，
	// 否则事后对比两次运行（或和 evalscope 对拍）时无法判断口径是否一致。
	ThinkTime       string `json:"think_time"`
	MaxOutputTokens int    `json:"max_output_tokens"`

	// 输入构造与运行形态同样影响可比性，一并留档
	PromptMode        string  `json:"prompt_mode"`
	PromptTokens      int     `json:"prompt_tokens,omitzero"`
	PromptTokensMin   int     `json:"prompt_tokens_min,omitzero"`
	PromptTokensMax   int     `json:"prompt_tokens_max,omitzero"`
	DatasetLines      int     `json:"dataset_lines,omitzero"`
	Tokenizer         string  `json:"tokenizer,omitzero"`
	TokenizerSHA256   string  `json:"tokenizer_sha256,omitzero"`
	UsageDriftPct     float64 `json:"usage_drift_pct,omitzero"`
	RequestsPerLevel  []int   `json:"requests_per_level,omitzero"`
	OpenLoopUnbounded bool    `json:"open_loop_unbounded,omitzero"`
	WarmupPerLevel    bool    `json:"warmup_per_level,omitzero"`
}

// jsonPercentileMs 是时延类指标的分位数（毫秒），供 JSON 序列化用。
type jsonPercentileMs struct {
	Min, P10, P25, P50, P75, P90, P95, P99, P995, P999, Max, Avg, StdDev float64
	N                                                                    int
}

// MarshalJSON 用显式结构体而非 map，保证字段顺序稳定（map 会按键名排序，
// 让 min/max 夹在分位数中间，人读 diff 时很别扭）。
func (p jsonPercentileMs) MarshalJSON() ([]byte, error) {
	if p.N == 0 {
		return []byte("null"), nil
	}
	type payload struct {
		Min    float64 `json:"min"`
		P10    float64 `json:"p10"`
		P25    float64 `json:"p25"`
		P50    float64 `json:"p50"`
		P75    float64 `json:"p75"`
		P90    float64 `json:"p90"`
		P95    float64 `json:"p95"`
		P99    float64 `json:"p99"`
		P995   float64 `json:"p995"`
		P999   float64 `json:"p999"`
		Max    float64 `json:"max"`
		Avg    float64 `json:"avg"`
		StdDev float64 `json:"stddev"`
		N      int     `json:"n"`
	}
	// 字段名、类型、顺序与 jsonPercentileMs 逐一对应，直接转换即可（tag 不参与可转换性判定）
	return json.Marshal(payload(p))
}

func percentileMsFromDuration(s types.PercentileStats) jsonPercentileMs {
	toMs := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	return jsonPercentileMs{
		Min: toMs(s.Min), P10: toMs(s.P10), P25: toMs(s.P25), P50: toMs(s.P50),
		P75: toMs(s.P75), P90: toMs(s.P90), P95: toMs(s.P95), P99: toMs(s.P99),
		P995: toMs(s.P995), P999: toMs(s.P999), Max: toMs(s.Max),
		Avg: toMs(s.Avg), StdDev: toMs(s.StdDev), N: s.N,
	}
}

type jsonLevel struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	TokenGroup   string  `json:"token_group"`
	Concurrency  int     `json:"concurrency"`
	TargetRateHz float64 `json:"target_rate_rps,omitzero"` // open-loop 档位的目标 RPS，closed-loop 为 0（省略）

	Start        time.Time      `json:"start"`
	WindowSec    float64        `json:"window_sec"`
	ElapsedSec   float64        `json:"elapsed_sec"`
	Total        int            `json:"total"`
	Success      int            `json:"success"`
	Failed       int            `json:"failed"`
	ErrorRatePct float64        `json:"error_rate_pct"`
	StoppedEarly bool           `json:"stopped_early"`
	RequestLimit int            `json:"request_limit,omitzero"` // 请求数制上限，0 为纯时长制
	ErrorCounts  map[string]int `json:"error_counts,omitzero"`

	TTFTMs jsonPercentileMs `json:"ttft_ms"`
	TPOTMs jsonPercentileMs `json:"tpot_ms"`
	ITLMs  jsonPercentileMs `json:"itl_ms"`
	E2EMs  jsonPercentileMs `json:"e2e_ms"`

	TPS       float64 `json:"tps"`
	TPM       float64 `json:"tpm"`
	QPS       float64 `json:"qps"`
	QPM       float64 `json:"qpm"`
	DecodeTPS float64 `json:"decode_tps,omitzero"` // 单流解码速度（1/平均 TPOT）

	// 稳态吞吐窗口：*_window_sec 为 0 表示 N/A（无时间轴或窗口不足 30s）
	SteadyQPS        float64 `json:"steady_qps,omitzero"`
	SteadyTPS        float64 `json:"steady_tps,omitzero"`
	SteadyWindowSec  float64 `json:"steady_window_sec,omitzero"`
	Last30sQPS       float64 `json:"last30s_qps,omitzero"`
	Last30sTPS       float64 `json:"last30s_tps,omitzero"`
	Last30sWindowSec float64 `json:"last30s_window_sec,omitzero"`

	// usage 对拍（需本地分词器）
	UsageChecked     int     `json:"usage_checked,omitzero"`
	UsageDrifted     int     `json:"usage_drifted,omitzero"`
	UsageDriftAbsP50 float64 `json:"usage_drift_abs_p50_pct,omitzero"`
	UsageDriftAbsMax float64 `json:"usage_drift_abs_max_pct,omitzero"`

	IORatio          float64 `json:"io_ratio,omitzero"`
	CacheHitRatioPct float64 `json:"cache_hit_ratio_pct,omitzero"`

	GoodputPct *float64 `json:"goodput_pct,omitempty"` // nil 表示本次运行未配置 slo
}

func toJSONLevel(agg types.AggregatedMetrics) jsonLevel {
	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}
	errCounts := make(map[string]int, len(agg.ErrorCounts))
	for et, n := range agg.ErrorCounts {
		if n > 0 {
			errCounts[string(et)] = n
		}
	}

	l := jsonLevel{
		Model:            agg.Model,
		Provider:         string(agg.Provider),
		TokenGroup:       agg.TokenGroup,
		Concurrency:      agg.Concurrency,
		TargetRateHz:     agg.TargetRate,
		Start:            agg.Start,
		WindowSec:        agg.Window.Seconds(),
		ElapsedSec:       agg.Elapsed.Seconds(),
		Total:            agg.Total,
		Success:          agg.Success,
		Failed:           agg.Failed,
		ErrorRatePct:     errPct,
		StoppedEarly:     agg.StoppedEarly,
		RequestLimit:     agg.RequestLimit,
		ErrorCounts:      errCounts,
		TTFTMs:           percentileMsFromDuration(agg.TTFT),
		TPOTMs:           percentileMsFromDuration(agg.TPOT),
		ITLMs:            percentileMsFromDuration(agg.ITL),
		E2EMs:            percentileMsFromDuration(agg.Latency),
		TPS:              agg.TPS,
		TPM:              agg.TPM,
		QPS:              agg.QPS,
		QPM:              agg.QPM,
		DecodeTPS:        agg.DecodeTPS,
		SteadyQPS:        agg.SteadyQPS,
		SteadyTPS:        agg.SteadyTPS,
		SteadyWindowSec:  agg.SteadyWindow.Seconds(),
		Last30sQPS:       agg.Last30sQPS,
		Last30sTPS:       agg.Last30sTPS,
		Last30sWindowSec: agg.Last30sWindow.Seconds(),
		UsageChecked:     agg.UsageChecked,
		UsageDrifted:     agg.UsageDrifted,
		UsageDriftAbsP50: agg.UsageDriftAbs.P50,
		UsageDriftAbsMax: agg.UsageDriftAbs.Max,
		IORatio:          agg.IORatio,
		CacheHitRatioPct: agg.CacheHitRatio,
	}
	if agg.SLOConfigured {
		v := agg.GoodputRatio
		l.GoodputPct = &v
	}
	return l
}

// promptModeName 返回配置的 prompt 生成方式名，与 YAML 里 prompt.mode 的取值一致。
func promptModeName(cfg types.BenchmarkConfig) string {
	switch {
	case cfg.DatasetPrompt:
		return "dataset"
	case cfg.DynamicPrompt:
		return "dynamic"
	case cfg.CodexPrompt:
		return "codex"
	default:
		return "text"
	}
}

// promptTokensIf 仅 dynamic 模式下返回目标 token 数，其他模式该值无意义。
func promptTokensIf(cfg types.BenchmarkConfig) int {
	if cfg.DynamicPrompt {
		return cfg.PromptTokens
	}
	return 0
}

// ExportJSON 把汇聚结果导出为结构化 JSON 报告，供 CI 里做基线比对、画趋势图。
func ExportJSON(cfg types.BenchmarkConfig, results []types.AggregatedMetrics, runAt time.Time, path string) error {
	report := jsonReport{
		RunAt: runAt,
		Config: jsonConfigSummary{
			BaseURL:         cfg.BaseURL,
			Duration:        cfg.Duration.String(),
			Concurrency:     cfg.Concurrency,
			LoadMode:        "closed",
			RequestRate:     cfg.RequestRate,
			ThinkTime:       cfg.ThinkTime.String(),
			MaxOutputTokens: cfg.EffectiveMaxOutputTokens(),

			PromptMode:        promptModeName(cfg),
			PromptTokens:      promptTokensIf(cfg),
			PromptTokensMin:   cfg.PromptTokensMin,
			PromptTokensMax:   cfg.PromptTokensMax,
			DatasetLines:      len(cfg.DatasetLines),
			Tokenizer:         cfg.TokenizerPath,
			TokenizerSHA256:   cfg.TokenizerFingerprint,
			UsageDriftPct:     cfg.UsageDriftPct,
			RequestsPerLevel:  cfg.RequestsPerLevel,
			OpenLoopUnbounded: cfg.OpenLoopUnbounded,
			WarmupPerLevel:    cfg.WarmupPerLevel,
		},
	}
	if cfg.OpenLoop {
		report.Config.LoadMode = "open"
	}
	for _, agg := range results {
		report.Levels = append(report.Levels, toJSONLevel(agg))
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 JSON 报告失败: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// csvHeaders 与 csvRow 逐档位一行，覆盖 CI 基线比对最常用的分位数/吞吐/错误率/goodput。
var csvHeaders = []string{
	"model", "provider", "token_group", "concurrency", "target_rate_rps",
	"total", "success", "failed", "error_rate_pct", "stopped_early",
	"ttft_p50_ms", "ttft_p90_ms", "ttft_p95_ms", "ttft_p99_ms", "ttft_stddev_ms",
	"tpot_p50_ms", "tpot_p90_ms", "tpot_p95_ms", "tpot_p99_ms",
	"itl_p50_ms", "itl_p90_ms", "itl_p95_ms", "itl_p99_ms",
	"e2e_p50_ms", "e2e_p90_ms", "e2e_p95_ms", "e2e_p99_ms", "e2e_stddev_ms",
	"qps", "tps", "decode_tps", "steady_qps", "steady_tps", "last30s_qps", "last30s_tps",
	"io_ratio", "cache_hit_ratio_pct", "goodput_pct", "usage_checked", "usage_drifted",
}

func csvRow(agg types.AggregatedMetrics) []string {
	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}
	toMs := func(d time.Duration) string {
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)
	}
	f2 := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	goodput := ""
	if agg.SLOConfigured {
		goodput = f2(agg.GoodputRatio)
	}
	return []string{
		agg.Model, string(agg.Provider), agg.TokenGroup, strconv.Itoa(agg.Concurrency), f2(agg.TargetRate),
		strconv.Itoa(agg.Total), strconv.Itoa(agg.Success), strconv.Itoa(agg.Failed), f2(errPct), strconv.FormatBool(agg.StoppedEarly),
		toMs(agg.TTFT.P50), toMs(agg.TTFT.P90), toMs(agg.TTFT.P95), toMs(agg.TTFT.P99), toMs(agg.TTFT.StdDev),
		toMs(agg.TPOT.P50), toMs(agg.TPOT.P90), toMs(agg.TPOT.P95), toMs(agg.TPOT.P99),
		toMs(agg.ITL.P50), toMs(agg.ITL.P90), toMs(agg.ITL.P95), toMs(agg.ITL.P99),
		toMs(agg.Latency.P50), toMs(agg.Latency.P90), toMs(agg.Latency.P95), toMs(agg.Latency.P99), toMs(agg.Latency.StdDev),
		f2(agg.QPS), f2(agg.TPS), f2(agg.DecodeTPS), f2(agg.SteadyQPS), f2(agg.SteadyTPS), f2(agg.Last30sQPS), f2(agg.Last30sTPS),
		f2(agg.IORatio), f2(agg.CacheHitRatio), goodput, strconv.Itoa(agg.UsageChecked), strconv.Itoa(agg.UsageDrifted),
	}
}

// ExportCSV 把汇聚结果导出为逐档位一行的 CSV 汇总，供 CI 基线比对/画趋势图。
func ExportCSV(results []types.AggregatedMetrics, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建 CSV 文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeaders); err != nil {
		return fmt.Errorf("写入 CSV 表头失败: %w", err)
	}
	for _, agg := range results {
		if err := w.Write(csvRow(agg)); err != nil {
			return fmt.Errorf("写入 CSV 行失败: %w", err)
		}
	}
	w.Flush()
	return w.Error()
}
