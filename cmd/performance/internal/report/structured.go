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
		IORatio:          agg.IORatio,
		CacheHitRatioPct: agg.CacheHitRatio,
	}
	if agg.SLOConfigured {
		v := agg.GoodputRatio
		l.GoodputPct = &v
	}
	return l
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
	"qps", "tps", "decode_tps", "io_ratio", "cache_hit_ratio_pct", "goodput_pct",
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
		f2(agg.QPS), f2(agg.TPS), f2(agg.DecodeTPS), f2(agg.IORatio), f2(agg.CacheHitRatio), goodput,
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
