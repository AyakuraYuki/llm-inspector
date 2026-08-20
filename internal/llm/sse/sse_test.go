package sse

import (
	"encoding/json"
	"testing"
)

func mustObj(t *testing.T, payload string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		t.Fatalf("fixture 解析失败: %v", err)
	}
	return obj
}

func TestParseSSELine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want map[string]any
	}{
		{"data 行", `data: {"a":1}`, map[string]any{"a": float64(1)}},
		{"带前后空格的 data 行", `  data: {"a":1}  `, map[string]any{"a": float64(1)}},
		{"空行", "", nil},
		{"注释行", `: keep-alive`, nil},
		{"event 行", `event: message_start`, nil},
		{"DONE 行", `data: [DONE]`, nil},
		{"非法 JSON", `data: not-json`, nil},
		{"非 data 非注释的裸 JSON", `{"a":1}`, map[string]any{"a": float64(1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSSELine(tt.line)
			if len(got) != len(tt.want) {
				t.Errorf("ParseSSELine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestIsSSEDoneLine(t *testing.T) {
	for _, line := range []string{"data: [DONE]", " data: [DONE] ", "[DONE]", "data:[DONE]"} {
		if !IsSSEDoneLine(line) {
			t.Errorf("IsSSEDoneLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"data: {}", "data: [done]", "data: {\"a\":1}"} {
		if IsSSEDoneLine(line) {
			t.Errorf("IsSSEDoneLine(%q) = true, want false", line)
		}
	}
}

func TestSSEIsTerminal(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{"openai finish_reason", `{"choices":[{"finish_reason":"stop"}]}`, true},
		{"openai finish_reason 为空", `{"choices":[{"finish_reason":""}]}`, false},
		{"openai 无 choices", `{"choices":[]}`, false},
		{"anthropic message_stop", `{"type":"message_stop"}`, true},
		{"anthropic message_delta stop_reason", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, true},
		{"anthropic message_delta 无 stop_reason", `{"type":"message_delta","delta":{}}`, false},
		{"gemini finishReason", `{"candidates":[{"finishReason":"STOP"}]}`, true},
		{"responses completed", `{"type":"response.completed","response":{}}`, true},
		{"responses incomplete", `{"type":"response.incomplete","response":{}}`, true},
		{"普通 delta", `{"type":"content_block_delta","delta":{"text":"hi"}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SSEIsTerminal(mustObj(t, tt.payload)); got != tt.want {
				t.Errorf("SSEIsTerminal = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSSEHasOutputContent(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{"openai content delta", `{"choices":[{"delta":{"content":"hi"}}]}`, true},
		{"openai reasoning_content", `{"choices":[{"delta":{"reasoning_content":"思考"}}]}`, true},
		{"openai reasoning 方言", `{"choices":[{"delta":{"reasoning":"思考"}}]}`, true},
		{"openai 空 delta", `{"choices":[{"delta":{}}]}`, false},
		{"anthropic text_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, true},
		{"anthropic thinking_delta", `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"思考"}}`, true},
		{"anthropic 空 delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}`, false},
		{"gemini parts text", `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`, true},
		{"gemini 空 parts", `{"candidates":[{"content":{"parts":[{"text":""}]}}]}`, false},
		{"responses output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`, true},
		{"responses reasoning_text.delta", `{"type":"response.reasoning_text.delta","delta":"思考"}`, true},
		{"message_start 元数据事件", `{"type":"message_start","message":{"usage":{"input_tokens":10}}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SSEHasOutputContent(mustObj(t, tt.payload)); got != tt.want {
				t.Errorf("SSEHasOutputContent = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSSEErrorInfo(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantMsg string
		wantOK  bool
	}{
		{"openai 顶层 error", `{"error":{"message":"boom"}}`, "boom", true},
		{"anthropic error 事件", `{"type":"error","error":{"message":"boom"}}`, "boom", true},
		{"responses error 事件", `{"type":"error","message":"boom"}`, "boom", true},
		{"responses response.failed", `{"type":"response.failed","response":{"error":{"message":"boom"}}}`, "boom", true},
		{"error 无 message 时序列化兜底", `{"error":{"code":500}}`, `{"code":500}`, true},
		{"无错误", `{"type":"content_block_delta"}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := SSEErrorInfo(mustObj(t, tt.payload))
			if ok != tt.wantOK || msg != tt.wantMsg {
				t.Errorf("SSEErrorInfo = (%q, %v), want (%q, %v)", msg, ok, tt.wantMsg, tt.wantOK)
			}
		})
	}
}

func TestConsumeSSEUsage_OpenAI(t *testing.T) {
	obj := mustObj(t, `{"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":5}}}`)
	var parts []string
	p, c, ct, source := ConsumeSSEUsage(obj, &parts)
	if p != 12 || c != 7 || ct != 5 || source != "usage" {
		t.Errorf("ConsumeSSEUsage = (%d, %d, %d, %q), want (12, 7, 5, usage)", p, c, ct, source)
	}
}

func TestConsumeSSEUsage_Anthropic(t *testing.T) {
	start := mustObj(t, `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":4}}}`)
	var parts []string
	p, c, ct, _ := ConsumeSSEUsage(start, &parts)
	if p != 10 || c != -1 || ct != 4 {
		t.Errorf("message_start = (%d, %d, %d), want (10, -1, 4)", p, c, ct)
	}
	delta := mustObj(t, `{"type":"message_delta","usage":{"output_tokens":7}}`)
	p, c, _, _ = ConsumeSSEUsage(delta, &parts)
	if p != 0 || c != 7 {
		t.Errorf("message_delta = (%d, %d), want (0, 7)", p, c)
	}
}

func TestConsumeSSEUsage_GeminiThoughts(t *testing.T) {
	obj := mustObj(t, `{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":1}}`)
	var parts []string
	p, c, ct, _ := ConsumeSSEUsage(obj, &parts)
	if p != 10 || c != 5 || ct != 1 { // candidates + thoughts = 5
		t.Errorf("ConsumeSSEUsage = (%d, %d, %d), want (10, 5, 1)", p, c, ct)
	}
}

func TestConsumeSSEUsage_Responses(t *testing.T) {
	obj := mustObj(t, `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`)
	var parts []string
	p, c, ct, source := ConsumeSSEUsage(obj, &parts)
	if p != 10 || c != 7 || ct != 3 || source != "usage" {
		t.Errorf("ConsumeSSEUsage = (%d, %d, %d, %q), want (10, 7, 3, usage)", p, c, ct, source)
	}
}

func TestApplySSEEvent_OpenAIFlow(t *testing.T) {
	s := NewStreamSummary()
	ApplySSEEvent(mustObj(t, `{"choices":[{"delta":{"role":"assistant"}}]}`), 10, s)
	ApplySSEEvent(mustObj(t, `{"choices":[{"delta":{"reasoning_content":"思考"}}]}`), 50, s)
	ApplySSEEvent(mustObj(t, `{"choices":[{"delta":{"content":"hi"}}]}`), 80, s)
	ApplySSEEvent(mustObj(t, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`), 100, s)
	ApplySSEEvent(mustObj(t, `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`), 110, s)

	if s.TTFTMS != 50 {
		t.Errorf("TTFTMS = %v, want 50（reasoning 首 chunk）", s.TTFTMS)
	}
	if !s.TerminalSeen || !s.UsageSeen {
		t.Errorf("TerminalSeen=%v UsageSeen=%v, want true", s.TerminalSeen, s.UsageSeen)
	}
	if s.PromptTokens != 12 || s.CompletionTokens != 7 {
		t.Errorf("tokens = (%d, %d), want (12, 7)", s.PromptTokens, s.CompletionTokens)
	}
	if len(s.TextParts) != 1 || s.TextParts[0] != "hi" {
		t.Errorf("TextParts = %v, want [hi]", s.TextParts)
	}
}

func TestApplySSEEvent_UpstreamErrorKeepsFirst(t *testing.T) {
	s := NewStreamSummary()
	ApplySSEEvent(mustObj(t, `{"error":{"message":"first"}}`), 10, s)
	ApplySSEEvent(mustObj(t, `{"error":{"message":"second"}}`), 20, s)
	if s.UpstreamErr != "first" {
		t.Errorf("UpstreamErr = %q, want first", s.UpstreamErr)
	}
}
