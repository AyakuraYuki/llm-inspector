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
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
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
	if cfg.Judge != nil {
		jp, err := provider.New(*cfg.Judge)
		if err != nil {
			return nil, err
		}
		judge = scorer.NewJudge(jp)
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
		fmt.Printf("执行 %s (%s)...\n", id, info.Name)
		lr = fn()
		lr.Compute(cfg.Thresholds.MinLayerScore)
		// 输出层级完成状态
		status := "✓"
		if !lr.Passed {
			status = "✗"
		}
		fmt.Printf("  %s %s 完成 (%.1f%%, %.2fs)\n", status, id, lr.Score*100, lr.DurationMS/1000)
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
		constraints := &protocol.ModelConstraints{
			DisableTemperatureZeroCheck: cfg.Target.Constraints.DisableTemperatureZeroCheck,
			SpecifiedTemperature:        cfg.Target.Constraints.SpecifiedTemperature,
			ThinkingEnableParams:        cfg.Target.Constraints.ThinkingEnableParams,
			ThinkingDisableParams:       cfg.Target.Constraints.ThinkingDisableParams,
			ReasoningEfforts:            cfg.Target.Constraints.ReasoningEfforts,
			DefaultMaxTokens:            cfg.Target.Constraints.DefaultMaxTokens,
		}
		return protocol.Run(ctx, p, cfg.Target.TokenizerConfig, constraints)
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

	// 总评：已执行且未跳过层的加权平均
	var sum, wSum float64
	allPassed := true
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
		if !l.Passed {
			allPassed = false
		}
	}
	if wSum > 0 {
		r.TotalScore = sum / wSum
	}
	r.FinishedAt = time.Now().Format(time.RFC3339)
	if r.Verdict == "" {
		r.Verdict = computeVerdict(executed, allPassed, r.TotalScore, cfg.Thresholds.MinLayerScore)
	}
	return r, nil
}

// computeVerdict 计算三档结论：
//   - pass：全部已执行层达标
//   - pass_with_warnings：总评达标但个别层未达标（模型可用，存在短板）
//   - fail：总评不达标（L1 门控失败时结论在门控处已置为 abort）
func computeVerdict(executed int, allPassed bool, totalScore, threshold float64) string {
	switch {
	case executed == 0:
		return "no_layers_executed"
	case allPassed:
		return "pass"
	case totalScore >= threshold:
		return "pass_with_warnings"
	default:
		return "fail"
	}
}

func catalogInfo(id string) (LayerInfo, bool) {
	for _, li := range Catalog() {
		if li.ID == id {
			return li, true
		}
	}
	return LayerInfo{}, false
}
