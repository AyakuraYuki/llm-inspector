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
)

// PrintCaseList 按分组列出筛选后的用例，供 -list 模式使用。
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

// Print 按用例顺序输出分组明细与汇总。
func Print(results []runner.Result) {
	fmt.Printf("\n================================ 测试报告 ================================\n")
	group := ""
	counts := map[runner.Verdict]int{}
	var fails []string
	for _, r := range results {
		if r.Group != group {
			group = r.Group
			fmt.Printf("\n[%s]\n", group)
		}
		status := "-"
		if r.HTTPStatus > 0 {
			status = fmt.Sprintf("%d", r.HTTPStatus)
		}
		latency := (time.Duration(r.LatencyMS) * time.Millisecond).Round(100 * time.Millisecond)
		fmt.Printf("  %-12s %-22s expect=%-8s http=%-3s %8s  %s\n",
			r.Verdict, r.CaseID, r.Expect, status, latency, r.Detail)
		counts[r.Verdict]++
		if r.Verdict == runner.VerdictFail {
			fails = append(fails, r.CaseID)
		}
	}

	fmt.Printf("\n--------------------------------------------------------------------------\n")
	fmt.Printf("总计 %d：PASS %d / FAIL %d / INCONCLUSIVE %d / INFO %d\n",
		len(results),
		counts[runner.VerdictPass], counts[runner.VerdictFail],
		counts[runner.VerdictInconclusive], counts[runner.VerdictInfo])
	if len(fails) > 0 {
		fmt.Printf("FAIL 用例: %s\n", strings.Join(fails, ", "))
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
