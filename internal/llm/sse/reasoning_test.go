package sse

import "testing"

func TestHasReasoningContent(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want bool
	}{
		{"anthropic thinking_delta", map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "thinking_delta", "thinking": "hmm"}}, true},
		{"anthropic text_delta", map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "hi"}}, false},
		{"openai reasoning_content", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "let me think"}}}}, true},
		{"openai reasoning", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning": "let me think"}}}}, true},
		{"openai content", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "answer"}}}}, false},
		{"gemini thought part", map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "思考", "thought": true}}}}}}, true},
		{"gemini plain part", map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "回答"}}}}}}, false},
		{"responses reasoning text", map[string]any{"type": "response.reasoning_text.delta", "delta": "x"}, true},
		{"responses output text", map[string]any{"type": "response.output_text.delta", "delta": "x"}, false},
	}
	for _, c := range cases {
		if got := HasReasoningContent(c.obj); got != c.want {
			t.Errorf("%s: HasReasoningContent = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestApplySSEEvent_SetsReasoningSeen(t *testing.T) {
	s := NewStreamSummary()
	ApplySSEEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "a"}}}}, 10, s)
	if s.ReasoningSeen {
		t.Fatalf("纯文本事件不应置 ReasoningSeen")
	}
	ApplySSEEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "b"}}}}, 20, s)
	if !s.ReasoningSeen {
		t.Fatalf("思考事件应置 ReasoningSeen")
	}
}
