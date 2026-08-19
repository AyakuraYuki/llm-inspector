package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// renderConsole 向 w 打印终端汇总（体检结论 + 逐层 + 接入结论）。
func renderConsole(w io.Writer, r *types.Report) {
	_, _ = fmt.Fprintf(w, "\n评测目标: %s  模型: %s\n", r.Target.BaseURL, r.Target.Model)
	_, _ = fmt.Fprintf(w, "开始时间: %s\n\n", r.StartedAt)

	_, _ = fmt.Fprintf(w, "体检结论:\n")
	for _, s := range r.Sections {
		_, _ = fmt.Fprintf(w, "  %-16s  %s  %s\n", s.Title+" ("+strings.Join(s.Layers, "/")+")",
			sectionStatusLabel(s.Status), sectionReasonSummary(s))
	}

	_, _ = fmt.Fprintf(w, "\n逐层:\n")
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

	_, _ = fmt.Fprintf(w, "\n接入结论: %s  主结论: %s\n", accessVerdict(r), VerdictLabel(r.Verdict))
	if r.Summary != nil && r.Summary.Status == "ok" {
		_, _ = fmt.Fprintf(w, "裁判总结: %s\n", oneLine(r.Summary.Text, 100))
	}
}

// renderMarkdown 生成 Markdown 报告正文。
func renderMarkdown(w io.Writer, r *types.Report) error {
	var sb strings.Builder

	sb.WriteString("# 模型接入体检报告\n\n")
	sb.WriteString("> " + oneLineSummary(r) + "\n\n")
	sb.WriteString(fmt.Sprintf("- 评测目标: `%s`\n- 模型: `%s`\n- 协议: `%s`\n- 开始时间: %s\n- 结束时间: %s\n\n",
		r.Target.BaseURL, r.Target.Model, r.Target.Protocol, r.StartedAt, r.FinishedAt))

	// 体检结论表
	sb.WriteString("## 体检结论\n\n")
	sb.WriteString("| 决策问题 | 结论 | 依据 |\n|---|---|---|\n")
	for _, s := range r.Sections {
		sb.WriteString(fmt.Sprintf("| %s（%s） | %s | %s |\n",
			s.Title, strings.Join(s.Layers, "/"),
			sectionStatusLabel(s.Status), sectionReasonSummary(s)))
	}
	sb.WriteString("\n")

	// 关键信号
	signals := extractKeySignals(r)
	if len(signals) > 0 {
		sb.WriteString("## 关键信号\n\n")
		for _, ks := range signals {
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", ks.Label, ks.Value))
		}
		sb.WriteString("\n")
	}

	// 逐层明细
	sb.WriteString("## 逐层明细\n\n")
	for _, l := range r.Layers {
		if !l.Enabled || l.Skipped || len(l.Checks) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n### %s %s\n\n", l.ID, l.Name))
		sb.WriteString("| 检查项 | 状态 | 得分 | 说明 |\n|---|---|---|---|\n")
		for _, c := range l.Checks {
			sb.WriteString(fmt.Sprintf("| %s | %s | %.2f | %s |\n", c.Name, checkStatus(c.Status), c.Score, escapeMD(oneLine(c.Detail, 200))))
		}
	}

	// 原始指标附录
	sb.WriteString("\n## 原始指标附录\n\n")
	if hasMetrics(r) {
		for _, l := range r.Layers {
			if !l.Enabled || l.Skipped || len(l.Checks) == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("\n<details><summary>%s %s 原始指标</summary>\n\n```json\n", l.ID, l.Name))
			sb.WriteString(metricsJSON(&l))
			sb.WriteString("\n```\n</details>\n")
		}
	} else {
		sb.WriteString("（无原始指标）\n")
	}

	// 裁判总结
	sb.WriteString("\n## 裁判总结\n\n")
	switch {
	case r.Summary == nil:
		sb.WriteString("*（未配置裁判模型，跳过总结）*\n")
	case r.Summary.Status == "ok":
		sb.WriteString("> " + r.Summary.Text + "\n")
	default:
		sb.WriteString(fmt.Sprintf("*（总结生成失败：%s）*\n", r.Summary.Error))
	}

	_, err := io.WriteString(w, sb.String())
	return err
}

func oneLineSummary(r *types.Report) string {
	access := sectionStatus(r.Sections, types.SectionAccess)
	perf := sectionStatus(r.Sections, types.SectionPerf)
	smoke := sectionStatus(r.Sections, types.SectionSmoke)
	return fmt.Sprintf("接入%s · 性能%s · 冒烟%s",
		sectionStatusLabel(access), sectionStatusLabel(perf), sectionStatusLabel(smoke))
}

func accessVerdict(r *types.Report) string {
	for _, s := range r.Sections {
		if s.Section == types.SectionAccess {
			switch s.Status {
			case "pass":
				return "可接入"
			case "fail":
				return "不满足接入条件"
			case "na":
				return "未评估"
			}
		}
	}
	return "未评估"
}

func sectionStatus(sections []types.SectionResult, sec types.ReportSection) string {
	for _, s := range sections {
		if s.Section == sec {
			return s.Status
		}
	}
	return "na"
}

func sectionStatusLabel(status string) string {
	switch status {
	case "pass":
		return "✅ 通过"
	case "warn":
		return "⚠️ 需关注"
	case "fail":
		return "❌ 不通过"
	default:
		return "➖ 未评估"
	}
}

// sectionReasonSummary 提炼结论依据：优先 Reasons，其次分数。
func sectionReasonSummary(s types.SectionResult) string {
	if len(s.Reasons) > 0 {
		return strings.Join(s.Reasons, "；")
	}
	if s.Status == "na" {
		return "未评估"
	}
	return fmt.Sprintf("得分 %.0f%%", s.Score*100)
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

func hasMetrics(r *types.Report) bool {
	for _, l := range r.Layers {
		for _, c := range l.Checks {
			if len(c.Metrics) > 0 {
				return true
			}
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
