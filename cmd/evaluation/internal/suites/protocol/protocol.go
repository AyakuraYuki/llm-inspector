// Package protocol 实现 L2：OpenAI 协议兼容性检查。
// 目标服务不支持的能力（如 JSON mode、tool calling）记为 unsupported，不计入层均分。
package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
)

// contentBudget 是内容型检查项的 max_tokens 预算。
// 思考型（reasoning）模型会先消耗 completion tokens 生成思考过程，
// 预算过小时正文为空串，会把"预算不足"误判成"能力缺失"。
// 与 L3 题目使用的预算（1024）保持一致；提示词均要求短答案，
// 对非思考型模型不会因此产生更长输出。
const contentBudget = 1024

// Run 执行 L2 全部检查。
func Run(ctx context.Context, p provider.Provider) core.LayerResult {
	start := time.Now()
	layer := core.LayerResult{ID: "L2", Name: "协议兼容性", Enabled: true}

	layer.Checks = append(layer.Checks,
		checkStreaming(ctx, p),
		checkSystemPrompt(ctx, p),
		checkMaxTokens(ctx, p),
		checkTemperatureZero(ctx, p),
		checkMultiTurn(ctx, p),
		checkJSONMode(ctx, p),
		checkToolCalling(ctx, p),
		checkUsageField(ctx, p),
	)
	layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return layer
}

func timed(name string, weight float64, fn func() core.CheckResult) core.CheckResult {
	start := time.Now()
	r := fn()
	r.Name = name
	r.Weight = weight
	r.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return r
}

func failScore(detail string) core.CheckResult {
	return core.CheckResult{Status: core.StatusFail, Score: 0, Detail: detail}
}

// checkStreaming 验证 SSE 流式：能收到增量 chunk、有 finish_reason、记录了 TTFT。
// 首 token 延迟占总耗时比例过高时在 detail/metrics 中提示（疑似伪流式或思考延迟），不影响得分。
func checkStreaming(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("streaming_sse", 2, func() core.CheckResult {
		resp, err := p.Stream(ctx, &provider.Request{
			Messages:  []provider.Message{{Role: "user", Content: "从 1 数到 10，用空格分隔"}},
			MaxTokens: contentBudget,
		})
		if err != nil {
			return failScore("流式请求失败: " + err.Error())
		}
		problems := []string{}
		if strings.TrimSpace(resp.Content) == "" {
			problems = append(problems, "聚合内容为空")
		}
		if resp.FinishReason == "" {
			problems = append(problems, "缺少 finish_reason")
		}
		if resp.TTFTMS < 0 {
			problems = append(problems, "未捕获到首 token")
		}
		status := core.StatusPass
		score := 1.0
		if len(problems) > 0 {
			status = core.StatusFail
			score = float64(3-len(problems)) / 3
		}
		metrics := map[string]any{
			"chunks":        resp.Chunks,
			"ttft_ms":       resp.TTFTMS,
			"finish_reason": resp.FinishReason,
		}
		detail := strings.Join(problems, "; ")
		// 伪流式/思考延迟提示：首内容 token 到达时几乎全部耗时已过去
		if resp.TTFTMS > 0 && resp.LatencyMS > 0 {
			ratio := resp.TTFTMS / resp.LatencyMS
			metrics["ttft_ratio"] = ratio
			if ratio > 0.9 && resp.LatencyMS > 2000 {
				note := fmt.Sprintf("首内容 token 占总耗时 %.0f%%（疑似伪流式转发或思考型模型，流式对降低体感延迟无效）", ratio*100)
				if detail == "" {
					detail = note
				} else {
					detail += "; " + note
				}
			}
		}
		return core.CheckResult{Status: status, Score: score, Detail: detail, Metrics: metrics}
	})
}

// checkSystemPrompt 验证 system 角色被接受并生效。
func checkSystemPrompt(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("system_prompt", 1, func() core.CheckResult {
		const prefix = "评测前缀"
		resp, err := p.Chat(ctx, &provider.Request{
			Messages: []provider.Message{
				{Role: "system", Content: fmt.Sprintf("你的所有回答必须以「%s：」开头，之后再说别的内容。", prefix)},
				{Role: "user", Content: "打个招呼"},
			},
			MaxTokens: contentBudget,
		})
		if err != nil {
			return failScore("带 system 角色的请求失败: " + err.Error())
		}
		if strings.Contains(resp.Content, prefix) {
			return core.CheckResult{Status: core.StatusPass, Score: 1, Detail: "system prompt 生效"}
		}
		if strings.TrimSpace(resp.Content) == "" {
			return core.CheckResult{Status: core.StatusPass, Score: 0.5,
				Detail: "system 角色被接受，但输出为空，无法验证前缀是否生效"}
		}
		return core.CheckResult{Status: core.StatusPass, Score: 0.5,
			Detail: "system 角色被接受，但模型未遵循前缀要求（可能是能力问题）"}
	})
}

// checkMaxTokens 验证 max_tokens 参数被尊重。
// 通过条件（满足其一）：finish_reason=length（截断标志生效）；
// usage 中的 completion_tokens 未超限（部分实现截断但不标 length）；
// usage 缺失时按字符数粗估未超限。
// 输出明显超限（如限 16 实际产出上千 tokens）说明参数被丢弃，判 fail——
// 这通常是网关未透传参数的真实缺陷。
func checkMaxTokens(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("max_tokens", 1, func() core.CheckResult {
		const limit = 16
		resp, err := p.Chat(ctx, &provider.Request{
			Messages:  []provider.Message{{Role: "user", Content: "写一首关于秋天的长诗，越长越好"}},
			MaxTokens: limit,
		})
		if err != nil {
			return failScore("请求失败: " + err.Error())
		}
		if resp.FinishReason == "length" {
			return core.CheckResult{Status: core.StatusPass, Score: 1,
				Detail: "finish_reason=length，截断生效"}
		}
		// 用 usage 判定；usage 缺失时按 1.5 字符/token 粗估
		used := resp.CompletionTokens
		estimated := false
		if used <= 0 {
			used = int64(float64(len([]rune(resp.Content))) / 1.5)
			estimated = true
		}
		if used <= int64(limit) {
			detail := fmt.Sprintf("输出约 %d tokens，未超限", used)
			if estimated {
				detail += "（按字符数估算，usage 缺失）"
			}
			return core.CheckResult{Status: core.StatusPass, Score: 1, Detail: detail}
		}
		return failScore(fmt.Sprintf("max_tokens=%d 未生效：输出 %d tokens（finish_reason=%q），参数疑似被平台丢弃",
			limit, used, resp.FinishReason))
	})
}

// checkTemperatureZero 验证 temperature 参数被接受，并观察 temperature=0 下的输出一致性。
// 注意：GPU 推理在 temp=0 下不保证逐位确定（批处理数值抖动是普遍现实），
// 因此本检查以"请求成功即参数被接受"为通过标准，一致率仅作为得分与指标呈现。
func checkTemperatureZero(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("temperature_zero", 1, func() core.CheckResult {
		const samples = 3
		zero := 0.0
		req := &provider.Request{
			Messages:    []provider.Message{{Role: "user", Content: "说一个 1 到 1000000 之间的整数，只输出数字"}},
			MaxTokens:   contentBudget,
			Temperature: &zero,
		}
		answers := map[string]int{}
		var errs []string
		empty := 0
		for range samples {
			resp, err := p.Chat(ctx, req)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			ans := strings.TrimSpace(resp.Content)
			if ans == "" {
				empty++ // 空输出不算一种答案，避免"全空"被误判为完全一致
				continue
			}
			answers[ans]++
		}
		if len(answers) == 0 {
			if empty > 0 {
				return failScore(fmt.Sprintf("%d 次采样输出全部为空，无法验证一致性", empty))
			}
			return failScore("全部请求失败: " + strings.Join(errs, "; "))
		}
		best, total := 0, 0
		for _, n := range answers {
			if n > best {
				best = n
			}
			total += n
		}
		agreement := float64(best) / float64(total)
		detail := fmt.Sprintf("%d 次采样全部一致", total)
		if len(answers) > 1 {
			detail = fmt.Sprintf("%d 次采样出现 %d 种输出（temp=0 下的数值抖动属常见服务侧行为，非协议缺陷）",
				total, len(answers))
		}
		if empty > 0 {
			detail += fmt.Sprintf("；%d 次输出为空", empty)
		}
		if len(errs) > 0 {
			detail += fmt.Sprintf("；%d 次请求失败", len(errs))
		}
		return core.CheckResult{Status: core.StatusPass, Score: agreement, Detail: detail,
			Metrics: map[string]any{
				"samples":   total,
				"distinct":  len(answers),
				"agreement": agreement,
				"empty":     empty,
				"errors":    len(errs),
			}}
	})
}

// checkMultiTurn 验证多轮 messages 数组被正确理解。
// 预算给到 contentBudget：思考型模型会先消耗 completion tokens 做思考，
// 预算过小时正文为空，会被误判为"多轮能力缺失"。
func checkMultiTurn(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("multi_turn", 1, func() core.CheckResult {
		resp, err := p.Chat(ctx, &provider.Request{
			Messages: []provider.Message{
				{Role: "user", Content: "记住暗号「蓝色狐狸」，只需回复「好的」。"},
				{Role: "assistant", Content: "好的"},
				{Role: "user", Content: "暗号是什么？只回答暗号本身。"},
			},
			MaxTokens: contentBudget,
		})
		if err != nil {
			return failScore("多轮请求失败: " + err.Error())
		}
		if strings.Contains(resp.Content, "蓝色狐狸") {
			return core.CheckResult{Status: core.StatusPass, Score: 1}
		}
		if strings.TrimSpace(resp.Content) == "" {
			return failScore(fmt.Sprintf(
				"输出为空，无法验证多轮记忆（finish_reason=%q，completion_tokens=%d；若为思考型模型，预算可能被思考过程耗尽）",
				resp.FinishReason, resp.CompletionTokens))
		}
		return failScore("模型未回忆起上一轮内容: " + truncate(resp.Content, 60))
	})
}

// checkJSONMode 验证 JSON 输出能力。openai/gemini 用原生 response_format 参数；
// anthropic 无原生 JSON mode，走 prompt 诱导并在 detail 中注明。
// 服务返回 400 记 unsupported。
func checkJSONMode(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("json_mode", 1, func() core.CheckResult {
		req := &provider.Request{
			Messages: []provider.Message{
				{Role: "user", Content: `输出一个 JSON 对象，包含字段 city（值为 "Paris"）。只输出 JSON，不要输出任何其他文字。`},
			},
			MaxTokens: contentBudget,
			JSONMode:  true,
		}
		promptInduced := false
		if p.Protocol() == "anthropic" {
			req.JSONMode = false // Anthropic 无原生 JSON mode 参数
			promptInduced = true
		}
		resp, err := p.Chat(ctx, req)
		if err != nil {
			if provider.StatusCode(err) == 400 {
				return core.CheckResult{Status: core.StatusUnsupported, Score: 0,
					Detail: "服务不支持 response_format json_object"}
			}
			return failScore("JSON mode 请求失败: " + err.Error())
		}
		s := stripFence(resp.Content)
		if json.Valid([]byte(s)) {
			detail := ""
			if promptInduced {
				detail = "通过 prompt 诱导实现（Anthropic 无原生 JSON mode 参数）"
			}
			return core.CheckResult{Status: core.StatusPass, Score: 1, Detail: detail}
		}
		return failScore("JSON 要求下输出不是合法 JSON: " + truncate(resp.Content, 80))
	})
}

// checkToolCalling 验证 tools 参数与工具调用响应结构。
// 三协议格式不同（openai tool_calls / anthropic tool_use / gemini functionCall），
// 由 provider 统一映射为 Result.ToolCalls。请求时强制调用一次（tool_choice=any），
// 避免"模型自主选择不调用"对协议兼容性判定的干扰。服务不支持记 unsupported。
func checkToolCalling(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("tool_calling", 1, func() core.CheckResult {
		resp, err := p.Chat(ctx, &provider.Request{
			Messages: []provider.Message{
				{Role: "user", Content: "巴黎现在天气怎么样？请使用提供的工具查询。"},
			},
			MaxTokens:   contentBudget,
			ToolsChoice: "any",
			Tools: []provider.Tool{{
				Name:        "get_weather",
				Description: "查询指定城市的当前天气",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string", "description": "城市名"},
					},
					"required": []string{"city"},
				},
			}},
		})
		if err != nil {
			if provider.StatusCode(err) == 400 {
				return core.CheckResult{Status: core.StatusUnsupported, Score: 0,
					Detail: "服务不支持 tools 参数"}
			}
			return failScore("tool calling 请求失败: " + err.Error())
		}
		if len(resp.ToolCalls) == 0 {
			return failScore("响应中没有工具调用（已强制 tool_choice=any，可能是协议未正确映射）")
		}
		tc := resp.ToolCalls[0]
		if tc.Name == "" || !json.Valid([]byte(tc.Arguments)) {
			return failScore(fmt.Sprintf("工具调用结构非法: name=%q arguments=%q", tc.Name, truncate(tc.Arguments, 60)))
		}
		return core.CheckResult{Status: core.StatusPass, Score: 1,
			Metrics: map[string]any{"tool": tc.Name, "arguments": tc.Arguments}}
	})
}

// checkUsageField 验证响应携带 usage 统计。
func checkUsageField(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("usage_field", 1, func() core.CheckResult {
		resp, err := p.Chat(ctx, &provider.Request{
			Messages:  []provider.Message{{Role: "user", Content: "说「你好」。"}},
			MaxTokens: 8,
		})
		if err != nil {
			return failScore("请求失败: " + err.Error())
		}
		if resp.PromptTokens > 0 && resp.CompletionTokens > 0 {
			return core.CheckResult{Status: core.StatusPass, Score: 1,
				Metrics: map[string]any{
					"prompt_tokens":     resp.PromptTokens,
					"completion_tokens": resp.CompletionTokens,
				}}
		}
		return failScore("响应缺少 usage 字段或为 0")
	})
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
