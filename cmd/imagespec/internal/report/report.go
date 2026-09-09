// Package report 负责 imagespec 的控制台报告与 JSON 报告输出。
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/cases"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

// PrintCaseList 按分组列出筛选后的用例，供 -list 模式使用。-list 不发起
// 任何请求也不产出报告，因此这里直接打印到终端，不经 logger 落盘。
func PrintCaseList(cs []cases.Case) {
	group := ""
	for _, c := range cs {
		if c.Group != group {
			group = c.Group
			fmt.Printf("\n[%s]\n", group)
		}
		params, _ := json.Marshal(c.Params)
		ps := string(params)
		if len(ps) > 100 {
			ps = ps[:100] + "...(truncated)"
		}
		fmt.Printf("  %-22s expect=%-8s %s", c.ID, c.Expect, ps)
		if c.Note != "" {
			fmt.Printf("  # %s", c.Note)
		}
		fmt.Println()
	}
	fmt.Printf("\n共 %d 个用例\n", len(cs))
}

// Print 按用例顺序输出分组明细与汇总，经 logger 同时落盘到运行日志（.txt）。
func Print(results []runner.Result) {
	logger.Printf("")
	logger.Printf("================================ 测试报告 ================================")
	group := ""
	counts := map[runner.Verdict]int{}
	var fails []string
	for _, r := range results {
		if r.Group != group {
			group = r.Group
			logger.Printf("")
			logger.Printf("[%s]", group)
		}
		status := "-"
		if r.HTTPStatus > 0 {
			status = fmt.Sprintf("%d", r.HTTPStatus)
		}
		latency := (time.Duration(r.LatencyMS) * time.Millisecond).Round(100 * time.Millisecond)
		logger.Printf("  %-12s %-22s expect=%-8s http=%-3s %8s  %s",
			r.Verdict, r.CaseID, r.Expect, status, latency, r.Detail)
		counts[r.Verdict]++
		if r.Verdict == runner.VerdictFail {
			fails = append(fails, r.CaseID)
		}
	}

	logger.Printf("")
	logger.Printf("--------------------------------------------------------------------------")
	logger.Printf("总计 %d：PASS %d / FAIL %d / INCONCLUSIVE %d / INFO %d",
		len(results),
		counts[runner.VerdictPass], counts[runner.VerdictFail],
		counts[runner.VerdictInconclusive], counts[runner.VerdictInfo])
	if len(fails) > 0 {
		logger.Printf("FAIL 用例: %s", strings.Join(fails, ", "))
	}
}

// HasFailure 报告结果中是否存在 FAIL 用例，用于决定进程退出码。
func HasFailure(results []runner.Result) bool {
	for _, r := range results {
		if r.Verdict == runner.VerdictFail {
			return true
		}
	}
	return false
}

// PrintSpeed 列出各用例的速度指标（单次采样的耗时分解，不是百分位统计），
// 经 logger 同时落盘。只展示确实生成了图片的用例（r.Speed != nil），方便按
// 尺寸/quality/是否流式横向比较；没有任何用例带图片时不打印这一节。
func PrintSpeed(results []runner.Result) {
	rows := make([]runner.Result, 0, len(results))
	for _, r := range results {
		if r.Speed != nil {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return
	}

	logger.Printf("")
	logger.Printf("================================ 速度指标（单次采样，非百分位统计） ================================")
	logger.Printf("  %-22s %-11s %8s %8s %3s %6s %10s %8s %s",
		"case", "size", "latency", "ms/img", "n", "MP", "ms/MP", "TTFPI", "备注")
	for _, r := range rows {
		size := "-"
		if len(r.Images) > 0 && r.Images[0].Width > 0 {
			size = fmt.Sprintf("%dx%d", r.Images[0].Width, r.Images[0].Height)
		}
		latency := (time.Duration(r.LatencyMS) * time.Millisecond).Round(10 * time.Millisecond)
		mp, msPerMP := "-", "-"
		if r.Speed.Megapixels > 0 {
			mp = fmt.Sprintf("%.2f", r.Speed.Megapixels)
			msPerMP = fmt.Sprintf("%.0f", r.Speed.MSPerMegapixel)
		}
		ttfpi, note := "-", ""
		if r.Speed.Streamed {
			ttfpi = fmt.Sprintf("%dms", r.Speed.TTFPIMS)
			if r.Speed.LikelyBuffered {
				note = "疑似假流式（首个 partial 几乎与总耗时同时到达）"
			}
		}
		logger.Printf("  %-22s %-11s %8s %8.0f %3d %6s %10s %8s %s",
			r.CaseID, size, latency, r.Speed.MSPerImage, len(r.Images), mp, msPerMP, ttfpi, note)
	}
}

// jsonReport 是 JSON 报告的顶层结构。
type jsonReport struct {
	BaseURL    string          `json:"base_url"`
	Model      string          `json:"model"`
	StartedAt  time.Time       `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Summary    map[string]int  `json:"summary"`
	Results    []runner.Result `json:"results"`
}

// WriteJSON 把完整结果（含请求体、响应片段、图片元信息）写入 path。
func WriteJSON(path string, cfg *config.Config, startAt time.Time, results []runner.Result) error {
	summary := map[string]int{}
	for _, r := range results {
		summary[string(r.Verdict)]++
	}
	data, err := json.MarshalIndent(jsonReport{
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		StartedAt:  startAt,
		DurationMS: time.Since(startAt).Milliseconds(),
		Summary:    summary,
		Results:    results,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
