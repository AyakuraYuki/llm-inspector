// Package summarizer 用裁判模型（judge）为评测报告生成中文总结。
package summarizer

import (
	"context"
	"fmt"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

const maxSummaryRunes = 300

// Summarizer 基于 judge provider 生成报告总结。
type Summarizer struct {
	p provider.Provider
}

// New 以给定端点创建总结器；p 为 nil 时返回 nil。
func New(p provider.Provider) *Summarizer {
	if p == nil {
		return nil
	}
	return &Summarizer{p: p}
}

// Summarize 输入结构化摘要（从 report.Sections + 各层 Score/TTFT 构建），
// 输出不超过 maxSummaryRunes 字的中文总结。
func (s *Summarizer) Summarize(ctx context.Context, report *types.Report) (string, error) {
	if s == nil || s.p == nil {
		return "", fmt.Errorf("未配置裁判模型")
	}
	prompt := buildPrompt(report)
	zero := 0.0
	resp, err := s.p.Chat(ctx, &params.Request{
		Messages:    []params.Message{{Role: "user", Content: prompt}},
		MaxTokens:   512,
		Temperature: &zero,
	})
	if err != nil {
		return "", fmt.Errorf("裁判模型调用失败: %w", err)
	}
	text := util.StripCodeFence(resp.Content)
	if text == "" {
		return "", fmt.Errorf("裁判模型输出为空")
	}
	r := []rune(text)
	if len(r) > maxSummaryRunes {
		text = string(r[:maxSummaryRunes]) + "…"
	}
	return text, nil
}

// buildPrompt 从结构化摘要构建裁判 prompt。
func buildPrompt(r *types.Report) string {
	access := sectionStatus(r.Sections, types.SectionAccess)
	perf := sectionStatus(r.Sections, types.SectionPerf)
	smoke := sectionStatus(r.Sections, types.SectionSmoke)

	ttftP99, ttftSLO := "?", "?"
	if l := findLayer(r.Layers, "L5"); l != nil {
		if c := findCheck(l, "latency_ttft"); c != nil {
			ttftP99 = metricString(c.Metrics, "ttft_p99_ms")
			ttftSLO = metricString(c.Metrics, "slo_ttft_p99_ms")
		}
	}

	return fmt.Sprintf(`你是模型接入体检报告的撰稿人。基于以下结构化评测结果，用中文写一段不超过 %d 字的总结，面向准备接入该模型的工程团队，回答三个问题：能否接入、性能是否达标、能力是否够用。

【评测结果】
目标: %s（协议 %s）
接入与合规(L1/L2/L6): %s，得分 %s
性能画像(L5): %s，TTFT P99 %sms（SLO %sms）
可用性冒烟(L3/L4): %s，得分 %s

请直接输出总结正文，不要标题、不要 Markdown 格式、不要 JSON。`,
		maxSummaryRunes,
		r.Target.Model, r.Target.Protocol,
		access, sectionScore(r.Sections, types.SectionAccess),
		perf, ttftP99, ttftSLO,
		smoke, sectionScore(r.Sections, types.SectionSmoke),
	)
}

func sectionStatus(sections []types.SectionResult, sec types.ReportSection) string {
	for _, s := range sections {
		if s.Section == sec {
			return s.Status
		}
	}
	return "na"
}

func sectionScore(sections []types.SectionResult, sec types.ReportSection) string {
	for _, s := range sections {
		if s.Section == sec {
			return fmt.Sprintf("%.0f%%", s.Score*100)
		}
	}
	return "0%"
}

func findLayer(layers []types.LayerResult, id string) *types.LayerResult {
	for i := range layers {
		if layers[i].ID == id {
			return &layers[i]
		}
	}
	return nil
}

func findCheck(l *types.LayerResult, name string) *types.CheckResult {
	for i := range l.Checks {
		if l.Checks[i].Name == name {
			return &l.Checks[i]
		}
	}
	return nil
}

func metricString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return "?"
	}
	switch t := v.(type) {
	case float64:
		return fmt.Sprintf("%.0f", t)
	case int:
		return fmt.Sprintf("%d", t)
	default:
		return fmt.Sprintf("%v", v)
	}
}
