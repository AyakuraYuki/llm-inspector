// Package runner 编排分层评测流水线：L1 门控 → L2~L5 顺序执行 → 汇总总评。
package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/scorer"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/availability"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/boundary"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/capability"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/performance"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/protocol"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/suites/stability"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/summarizer"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// Version 工具版本号。
const Version = "0.5.0"

// LayerInfo 描述一层及其检查项，供 list 命令展示。
type LayerInfo struct {
	ID     string
	Name   string
	Checks []string
}

// Catalog 返回全部层的目录信息。
func Catalog() []LayerInfo {
	return []LayerInfo{
		{"L1", "API 可用性", []string{"models_endpoint", "minimal_chat", "error_semantics", "model_listed"}},
		{"L2", "协议兼容性", []string{
			"streaming_sse", "system_prompt", "max_tokens", "temperature_zero", "multi_turn", "json_mode", "tool_calling", "usage_field",
			"stop_sequence", "seed_consistency", "stream_usage_options", "encoding_unicode",
			"json_schema", "parallel_tool_calls", "tool_result_round_trip", "thinking_control", "reasoning_effort", "default_max_tokens", "no_default_system_prompt",
		}},
		{"L3", "模型能力", []string{"按数据集逐题执行（内建题库约 21 题，可通过配置文件设置特定数据集文件替换）"}},
		{"L4", "稳定性", []string{"self_consistency", "prompt_perturbation", "soak_test", "adversarial_inputs"}},
		{"L5", "模型性能", []string{"latency_ttft", "throughput", "concurrency_scaling", "context_probe"}},
		{"L6", "参数边界与健壮性", []string{
			"messages_boundary", "top_p_boundary", "frequency_penalty_boundary", "presence_penalty_boundary",
			"temperature_boundary", "max_tokens_boundary", "max_completion_tokens_compat", "auth_boundary",
		}},
	}
}

// Run 执行完整评测流水线。
func Run(ctx context.Context, cfg *config.Config) (*types.Report, error) {
	p, err := provider.New(cfg.Target)
	if err != nil {
		return nil, err
	}
	badKey, err := provider.New(cfg.Target.WithAPIKey("sk-invalid-key-for-eval"))
	if err != nil {
		return nil, err
	}

	var judge *scorer.Judge
	var judgeProvider provider.Provider
	if cfg.Judge != nil {
		judgeProvider, err = provider.New(*cfg.Judge)
		if err != nil {
			return nil, err
		}
		judge = scorer.NewJudge(judgeProvider)
	}

	startedAt := time.Now()
	r := &types.Report{
		Tool:      cfg.Tool,
		Version:   Version,
		Target:    types.TargetInfo{BaseURL: cfg.Target.BaseURL, Model: cfg.Target.Model, Protocol: p.Protocol()},
		StartedAt: startedAt.Format(time.RFC3339),
	}

	skipRest := false
	skipReason := ""

	runLayer := func(id string, enabled *bool, fn func() types.LayerResult) {
		lr := types.LayerResult{ID: id}
		if !util.Enabled(enabled) {
			if info, ok := catalogInfo(id); ok {
				lr.Name = info.Name
			}
			lr.Enabled = false
			r.Layers = append(r.Layers, lr)
			return
		}
		if skipRest {
			if info, ok := catalogInfo(id); ok {
				lr.Name = info.Name
			}
			lr.Enabled = true
			lr.Skipped = true
			lr.Reason = skipReason
			r.Layers = append(r.Layers, lr)
			return
		}
		// 输出当前层级进度
		info, _ := catalogInfo(id)
		logger.Printf("执行 %s (%s)...", id, info.Name)
		lr = fn()
		lr.Compute(cfg.Thresholds.MinLayerScore)
		// 输出层级完成状态
		status := "✓"
		if !lr.Passed {
			status = "✗"
		}
		logger.Printf("  %s %s 完成 (%.1f%%, %.2fs)", status, id, lr.Score*100, lr.DurationMS/1000)
		r.Layers = append(r.Layers, lr)
		if id == "L1" && lr.HasFail() {
			skipRest = true
			skipReason = "L1 门控未通过，中止后续评测"
			r.Verdict = "abort"
		} else if cfg.Thresholds.FailFast && !lr.Passed {
			skipRest = true
			skipReason = fmt.Sprintf("%s 未达标且 fail_fast 开启", id)
		}
	}

	runLayer("L1", cfg.Layers.Availability.Enabled, func() types.LayerResult {
		return availability.Run(ctx, p, badKey)
	})
	runLayer("L2", cfg.Layers.Protocol.Enabled, func() types.LayerResult {
		return protocol.Run(ctx, p, cfg.Target.TokenizerConfig, &cfg.Target.Constraints)
	})
	runLayer("L3", cfg.Layers.Capability.Enabled, func() types.LayerResult {
		return capability.Run(ctx, p, cfg.Layers.Capability, judge)
	})
	runLayer("L4", cfg.Layers.Stability.Enabled, func() types.LayerResult {
		return stability.Run(ctx, p, cfg.Layers.Stability)
	})
	runLayer("L5", cfg.Layers.Performance.Enabled, func() types.LayerResult {
		return performance.Run(ctx, p, cfg.Layers.Performance)
	})
	runLayer("L6", cfg.Layers.Boundary.Enabled, func() types.LayerResult {
		return boundary.Run(ctx, p)
	})

	// 三条体检结论（先于总结与 verdict 计算）
	r.Sections = types.ComputeSections(r.Layers, cfg.Thresholds.MinLayerScore)

	// 裁判总结：judge 未配置时 Summary 保持 nil（渲染端按 skipped 处理）；
	// 生成失败不影响退出码，只写进 Summary.Error。
	if judge != nil && r.Verdict != "abort" {
		s := summarizer.New(judgeProvider)
		sumCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		text, err := s.Summarize(sumCtx, r)
		cancel()
		switch {
		case err == nil:
			r.Summary = &types.JudgeSummary{Status: "ok", Text: text, Model: cfg.Judge.Model}
		default:
			r.Summary = &types.JudgeSummary{Status: "error", Error: err.Error()}
		}
	}

	// 总评参考分：已执行且未跳过层的加权平均（仅供兼容展示，不参与判定）
	var sum, wSum float64
	executed := 0
	for _, l := range r.Layers {
		if !l.Enabled || l.Skipped {
			continue
		}
		executed++
		w := types.LayerWeight[l.ID]
		if w <= 0 {
			w = 1
		}
		sum += l.Score * w
		wSum += w
	}
	if wSum > 0 {
		r.TotalScore = sum / wSum
	}
	r.FinishedAt = time.Now().Format(time.RFC3339)
	if r.Verdict == "" {
		r.Verdict = deriveVerdict(executed, r.Sections)
	}
	return r, nil
}

// deriveVerdict 由三条体检结论推导主结论。
//   - abort / no_layers_executed 在门控与执行阶段已置入，此处只处理其余情况。
//   - 接入结论 fail → fail；接入 pass 且冒烟 pass → pass；
//     接入 pass 且冒烟 warn → pass_with_warnings；
//     接入 na（如只跑 L5）→ 有任意层执行时视为诊断用途，判 pass。
func deriveVerdict(executed int, sections []types.SectionResult) string {
	if executed == 0 {
		return "no_layers_executed"
	}
	access := sectionStatus(sections, types.SectionAccess)
	smoke := sectionStatus(sections, types.SectionSmoke)
	switch {
	case access == "fail":
		return "fail"
	case access == "pass" && (smoke == "pass" || smoke == "na"):
		// smoke=na 表示冒烟层未执行（partial 运行），不算短板
		return "pass"
	case access == "pass" && smoke == "warn":
		return "pass_with_warnings"
	case access == "na":
		// 只跑了性能或其他非接入层：诊断用途，不判失败
		return "pass"
	default:
		// access 为 warn（理论上不出现，接入无 warn 档）时保守判 fail
		return "fail"
	}
}

// sectionStatus 返回指定结论的状态；未找到返回 "na"。
func sectionStatus(sections []types.SectionResult, sec types.ReportSection) string {
	for _, s := range sections {
		if s.Section == sec {
			return s.Status
		}
	}
	return "na"
}

func catalogInfo(id string) (LayerInfo, bool) {
	for _, li := range Catalog() {
		if li.ID == id {
			return li, true
		}
	}
	return LayerInfo{}, false
}
