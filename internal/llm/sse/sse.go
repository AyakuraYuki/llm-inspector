// Package sse 提供 OpenAI 兼容生态 SSE 流的协议无关解析原语。
//
// 本包从 cmd/performance 的流式解析器提取（performance 已完整使用），
// evaluation 仅复用部分常量（ReasoningDialects）。函数均为纯函数：
// 输入 SSE 行或已解析的 JSON 对象，输出判定结果/提取值，无状态、无 I/O。
package sse

import (
	"encoding/json"
	"strings"
)

// ReasoningDialects 是 openai 兼容网关承载思考内容的字段名方言，按优先级排列。
// reasoning_content 为 DeepSeek-R1 及多数网关的写法，reasoning 为部分网关的写法。
var ReasoningDialects = []string{"reasoning_content", "reasoning"}

// IsDoneLine 判断是否为 OpenAI 兼容协议的流终止标记行（data: [DONE]）。
func IsDoneLine(line string) bool {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "data:") {
		line = strings.TrimSpace(line[5:])
	}
	return line == "[DONE]"
}

// IsTerminal 判断 SSE 事件是否为协议定义的正常终止信号。各协议在生成结束时
// 必然给出终止标记：OpenAI 兼容协议为 choices[].finish_reason（外加其后的 [DONE]
// 行，见 IsDoneLine）；Anthropic 为 message_delta.delta.stop_reason 与
// message_stop；Gemini 为 candidates[].finishReason；Responses API 为
// response.completed / response.incomplete。全程未见任何终止标记的流按截断处理。
func IsTerminal(obj map[string]any) bool {
	switch t, _ := obj["type"].(string); t {
	case "message_stop", "response.completed", "response.incomplete":
		return true
	case "message_delta":
		if delta, ok := obj["delta"].(map[string]any); ok {
			if sr, _ := delta["stop_reason"].(string); sr != "" {
				return true
			}
		}
	}
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if fr, _ := c["finish_reason"].(string); fr != "" {
				return true
			}
		}
	}
	if candidates, ok := obj["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if fr, _ := cand["finishReason"].(string); fr != "" {
				return true
			}
		}
	}
	return false
}

// ErrorInfo 识别流内的错误事件。网关常在 HTTP 200 建流后才发现上游失败，
// 只能以错误事件形式发回：OpenAI 兼容协议/Gemini 为顶层 {"error":{...}}，
// Anthropic 为 {"type":"error","error":{...}}，Responses API 为
// {"type":"error",...} 或 {"type":"response.failed","response":{"error":{...}}}。
func ErrorInfo(obj map[string]any) (msg string, found bool) {
	if e, ok := obj["error"].(map[string]any); ok {
		return extractErrorMessage(e), true
	}
	switch t, _ := obj["type"].(string); t {
	case "error":
		if m, _ := obj["message"].(string); m != "" {
			return m, true
		}
		return "error event", true
	case "response.failed":
		if r, ok := obj["response"].(map[string]any); ok {
			if e, ok := r["error"].(map[string]any); ok {
				return extractErrorMessage(e), true
			}
		}
		return "response.failed", true
	}
	return "", false
}

// extractErrorMessage 从错误对象中提取 message，取不到时序列化整个对象兜底。
func extractErrorMessage(e map[string]any) string {
	if m, _ := e["message"].(string); m != "" {
		return m
	}
	b, _ := json.Marshal(e)
	return string(b)
}

// ParseLine 将 SSE 数据行解析为 JSON 对象，遇到空行/注释/[DONE] 返回 nil。
func ParseLine(line string) map[string]any {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return nil
	}
	payload := line
	if strings.HasPrefix(line, "data:") {
		payload = strings.TrimSpace(line[5:])
	}
	if payload == "[DONE]" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return nil
	}
	return obj
}

// HasOutputContent 检测 SSE 对象是否包含可视文本输出或思考内容（用于标记 TTFT 时刻）。
//
// 推理类模型（Claude extended thinking、DeepSeek-R1 系 reasoning_content、
// Gemini thinking 等）会先流式输出思考内容，再输出正式回答。思考内容同样是
// 用户/调用方能感知到的首个 token，因此也应计入 TTFT，而不能等到正式回答
// 开始才打点——否则开启了思考的请求会被算出偏高、失真的 TTFT。
func HasOutputContent(obj map[string]any) bool {
	// Anthropic: content_block_delta，delta.text 为正式回答，
	// delta.type == "thinking_delta" 时 delta.thinking 为思考内容
	if t, _ := obj["type"].(string); t == "content_block_delta" {
		if delta, ok := obj["delta"].(map[string]any); ok {
			if text, _ := delta["text"].(string); text != "" {
				return true
			}
			if thinking, _ := delta["thinking"].(string); thinking != "" {
				return true
			}
		}
	}
	// OpenAI 兼容协议: choices[].delta.content 为正式回答，
	// delta.reasoning_content（DeepSeek-R1/多数网关）或 delta.reasoning（部分网关）为思考内容
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if delta, ok := c["delta"].(map[string]any); ok {
				if content, _ := delta["content"].(string); content != "" {
					return true
				}
				for _, dialect := range ReasoningDialects {
					if reasoning, _ := delta[dialect].(string); reasoning != "" {
						return true
					}
				}
			}
		}
	}
	// Gemini: candidates[].content.parts[].text
	// 思考内容同样落在 parts[].text 里（配合 thought:true 标记），此处按文本非空
	// 统一判断，天然覆盖思考内容，无需额外分支；逐个扫描所有 parts，
	// 避免首个 part 为空文本时漏判
	if candidates, ok := obj["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					for _, p := range parts {
						if part, ok := p.(map[string]any); ok {
							if text, _ := part["text"].(string); text != "" {
								return true
							}
						}
					}
				}
			}
		}
	}
	// OpenAI Responses API: response.output_text.delta 为正式回答，
	// response.reasoning_summary_text.delta / response.reasoning_text.delta 为思考内容
	if t, _ := obj["type"].(string); t == "response.output_text.delta" ||
		t == "response.reasoning_summary_text.delta" ||
		t == "response.reasoning_text.delta" {
		if delta, _ := obj["delta"].(string); delta != "" {
			return true
		}
	}
	return false
}

// ConsumeUsage 从 SSE 对象中提取 usage（completion_tokens/output_tokens/cached_tokens）和文本片段。
// compT == -1 表示本事件无 usage 信息；cachedT == -1 表示本事件未携带缓存命中信息。
// promptT 在各协议下统一为「全部输入上下文」口径：OpenAI/Gemini 的输入计数本身
// 包含缓存命中部分；Anthropic 的 input_tokens 不含缓存读/写 token，这里补回，
// 保证缓存命中率 cachedT/promptT ≤ 100% 且跨协议可比。
func ConsumeUsage(obj map[string]any, textParts *[]string) (promptT, compT, cachedT int64, source string) {
	compT = -1
	cachedT = -1

	// OpenAI 顶层 usage（stream_options include_usage 的最终 chunk）
	if usage, ok := obj["usage"].(map[string]any); ok {
		c := IntFromMap(usage, "completion_tokens", "output_tokens")
		if c >= 0 {
			compT = c
			promptT = max(int64(0), IntFromMap(usage, "prompt_tokens", "input_tokens"))
			source = "usage"
		}
		// OpenAI: usage.prompt_tokens_details.cached_tokens
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			cachedT = IntFromMap(details, "cached_tokens")
		}
	}

	// Anthropic message_delta 事件里的 usage
	if compT < 0 {
		if t, _ := obj["type"].(string); t == "message_delta" {
			if usage, ok := obj["usage"].(map[string]any); ok {
				c := IntFromMap(usage, "output_tokens")
				if c >= 0 {
					compT = c
					source = "usage"
				}
			}
		}
	}

	// Anthropic message_start 事件里的初始 usage（携带 input_tokens 和缓存读取 token 数，此时尚无 output_tokens）
	if t, _ := obj["type"].(string); t == "message_start" {
		if msg, ok := obj["message"].(map[string]any); ok {
			if usage, ok := msg["usage"].(map[string]any); ok {
				cacheRead := IntFromMap(usage, "cache_read_input_tokens")
				if p := IntFromMap(usage, "input_tokens"); p >= 0 {
					// Anthropic 口径的 input_tokens 不含缓存读/写 token（与 OpenAI
					// prompt_tokens 含 cached_tokens 不同），补回这两部分对齐口径。
					promptT = p + max(cacheRead, 0) + cacheCreationTokens(usage)
				}
				cachedT = cacheRead
			}
		}
	}

	// Gemini: usageMetadata.candidatesTokenCount / thoughtsTokenCount / cachedContentTokenCount
	if meta, ok := obj["usageMetadata"].(map[string]any); ok {
		if compT < 0 {
			c := IntFromMap(meta, "candidatesTokenCount")
			// candidatesTokenCount 不含思考 token（Gemini 单独记在 thoughtsTokenCount），
			// 而 OpenAI 的 completion_tokens、Anthropic 的 output_tokens 均含思考 token。
			// 思考时间在生成窗口里、token 却不在分母里会系统性抬高 TPOT、压低 TPS，
			// 这里补上思考 token 保证跨协议可比。
			th := IntFromMap(meta, "thoughtsTokenCount")
			if c >= 0 || th > 0 {
				compT = max(c, 0) + max(th, 0)
				promptT = max(int64(0), IntFromMap(meta, "promptTokenCount"))
				source = "usage"
			}
		}
		cachedT = IntFromMap(meta, "cachedContentTokenCount")
	}

	// OpenAI Responses API: response.completed → response.usage
	if compT < 0 {
		if t, _ := obj["type"].(string); t == "response.completed" {
			if r, ok := obj["response"].(map[string]any); ok {
				if usage, ok := r["usage"].(map[string]any); ok {
					c := IntFromMap(usage, "output_tokens")
					if c >= 0 {
						compT = c
						promptT = max(int64(0), IntFromMap(usage, "input_tokens"))
						source = "usage"
					}
					// OpenAI Responses API: usage.input_tokens_details.cached_tokens
					if details, ok := usage["input_tokens_details"].(map[string]any); ok {
						cachedT = IntFromMap(details, "cached_tokens")
					}
				}
			}
		}
	}

	// 收集文本片段（用于 token 估算回退）
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if delta, ok := c["delta"].(map[string]any); ok {
				if content, _ := delta["content"].(string); content != "" {
					*textParts = append(*textParts, content)
				}
			}
		}
	}

	if delta, ok := obj["delta"].(map[string]any); ok {
		if text, _ := delta["text"].(string); text != "" {
			*textParts = append(*textParts, text)
		}
	}

	if candidates, ok := obj["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					for _, p := range parts {
						if part, ok := p.(map[string]any); ok {
							if text, _ := part["text"].(string); text != "" {
								*textParts = append(*textParts, text)
							}
						}
					}
				}
			}
		}
	}

	// OpenAI Responses API: response.output_text.delta
	if t, _ := obj["type"].(string); t == "response.output_text.delta" {
		if delta, _ := obj["delta"].(string); delta != "" {
			*textParts = append(*textParts, delta)
		}
	}

	return
}

// IntFromMap 按顺序查找 keys，返回第一个非负整数，否则返回 -1。
func IntFromMap(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			return int64(x)
		case int:
			return int64(x)
		}
	}
	return -1
}

// cacheCreationTokens 提取 Anthropic 缓存写入 token 数，兼容两种上报形态：
// 顶层整型 cache_creation_input_tokens（旧版）与嵌套对象
// cache_creation.ephemeral_*_input_tokens（新版按 TTL 拆分）。
func cacheCreationTokens(usage map[string]any) int64 {
	if v := IntFromMap(usage, "cache_creation_input_tokens"); v > 0 {
		return v
	}
	if cc, ok := usage["cache_creation"].(map[string]any); ok {
		return max(IntFromMap(cc, "ephemeral_5m_input_tokens"), 0) +
			max(IntFromMap(cc, "ephemeral_1h_input_tokens"), 0)
	}
	return 0
}
