// Package report 负责评测报告的终端展示与文件输出（JSON / Markdown）。
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// Console 向 w 打印终端汇总。
func Console(w io.Writer, r *types.Report) {
	_, _ = fmt.Fprintf(w, "\n评测目标: %s  模型: %s\n", r.Target.BaseURL, r.Target.Model)
	_, _ = fmt.Fprintf(w, "开始时间: %s\n\n", r.StartedAt)
	for _, l := range r.Layers {
		switch {
		case !l.Enabled:
			_, _ = fmt.Fprintf(w, "  %s %-12s  [未启用]\n", l.ID, l.Name)
		case l.Skipped:
			_, _ = fmt.Fprintf(w, "  %s %-12s  [跳过] %s\n", l.ID, l.Name, l.Reason)
		default:
			status := "PASS"
			if !l.Passed {
				status = "FAIL"
			}
			_, _ = fmt.Fprintf(w, "  %s %-12s  得分 %5.1f%%  [%s]  (%d 项检查, %.1fs)\n",
				l.ID, l.Name, l.Score*100, status, len(l.Checks), l.DurationMS/1000)
			for _, c := range l.Checks {
				if c.Status == types.StatusFail {
					_, _ = fmt.Fprintf(w, "      ✗ %s: %s\n", c.Name, oneLine(c.Detail, 100))
				}
			}
		}
	}
	_, _ = fmt.Fprintf(w, "\n总评: %.1f%%  结论: %s\n", r.TotalScore*100, VerdictLabel(r.Verdict))
}

// VerdictLabel 返回结论的可读标注。
func VerdictLabel(v string) string {
	switch v {
	case "pass":
		return "pass（全部层达标）"
	case "pass_with_warnings":
		return "pass_with_warnings（总评达标，个别层存在短板）"
	case "abort":
		return "abort（L1 门控未通过，已中止）"
	case "no_layers_executed":
		return "no_layers_executed（未执行任何层）"
	default:
		return v
	}
}

// Save 按 formats 将报告写入 dir/<timestamp>/ 下，返回输出目录。
func Save(dir string, formats []string, r *types.Report) (string, error) {
	outDir := filepath.Join(dir, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("创建报告目录失败: %w", err)
	}
	for _, f := range formats {
		switch strings.ToLower(f) {
		case "json":
			if err := writeJSON(filepath.Join(outDir, "report.json"), r); err != nil {
				return "", err
			}
		case "markdown", "md":
			if err := writeMarkdown(filepath.Join(outDir, "report.md"), r); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("未知报告格式: %q", f)
		}
	}
	return outDir, nil
}

func writeJSON(path string, r *types.Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeMarkdown(path string, r *types.Report) error {
	var sb strings.Builder
	sb.WriteString("# LLM 可用性评测报告\n\n")
	_, _ = fmt.Fprintf(&sb, "- 评测目标: `%s`\n- 模型: `%s`\n- 开始时间: %s\n- 结束时间: %s\n", r.Target.BaseURL, r.Target.Model, r.StartedAt, r.FinishedAt)
	_, _ = fmt.Fprintf(&sb, "- **总评: %.1f%%  结论: %s**\n\n", r.TotalScore*100, VerdictLabel(r.Verdict))

	sb.WriteString("## 分层汇总\n\n")
	sb.WriteString("| 层 | 名称 | 得分 | 状态 | 检查项数 | 耗时 |\n")
	sb.WriteString("|---|---|---|---|---|---|\n")
	for _, l := range r.Layers {
		status := layerStatus(&l)
		_, _ = fmt.Fprintf(&sb, "| %s | %s | %.1f%% | %s | %d | %.1fs |\n", l.ID, l.Name, l.Score*100, status, len(l.Checks), l.DurationMS/1000)
	}

	for _, l := range r.Layers {
		if !l.Enabled || l.Skipped || len(l.Checks) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(&sb, "\n## %s %s\n\n", l.ID, l.Name)
		sb.WriteString("| 检查项 | 状态 | 得分 | 说明 |\n")
		sb.WriteString("|---|---|---|---|\n")
		for _, c := range l.Checks {
			_, _ = fmt.Fprintf(&sb, "| %s | %s | %.2f | %s |\n", c.Name, checkStatus(c.Status), c.Score, escapeMD(oneLine(c.Detail, 200)))
		}
		if hasMetrics(&l) {
			sb.WriteString("\n<details><summary>原始指标</summary>\n\n```json\n")
			sb.WriteString(metricsJSON(&l))
			sb.WriteString("\n```\n</details>\n")
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func layerStatus(l *types.LayerResult) string {
	switch {
	case !l.Enabled:
		return "未启用"
	case l.Skipped:
		return "跳过: " + l.Reason
	case l.Passed:
		return "✅ PASS"
	default:
		return "❌ FAIL"
	}
}

func checkStatus(s types.Status) string {
	switch s {
	case types.StatusPass:
		return "✅"
	case types.StatusFail:
		return "❌"
	case types.StatusUnsupported:
		return "➖ 不支持"
	default:
		return "⏭️ 跳过"
	}
}

func hasMetrics(l *types.LayerResult) bool {
	for _, c := range l.Checks {
		if len(c.Metrics) > 0 {
			return true
		}
	}
	return false
}

func metricsJSON(l *types.LayerResult) string {
	m := map[string]any{}
	for _, c := range l.Checks {
		if len(c.Metrics) > 0 {
			m[c.Name] = c.Metrics
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

func escapeMD(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
