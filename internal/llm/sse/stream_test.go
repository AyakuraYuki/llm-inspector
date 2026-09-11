package sse

import "testing"

func TestApplySSEEvent_ITLSamples(t *testing.T) {
	s := NewStreamSummary()

	// 第一个内容事件打 TTFT，不产生 ITL 样本
	ApplySSEEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "a"}}}}, 100, s)
	if s.TTFTMS != 100 {
		t.Fatalf("TTFTMS = %v, want 100", s.TTFTMS)
	}
	if len(s.ITLSamplesMS) != 0 {
		t.Fatalf("首个内容事件不应产生 ITL 样本，got %v", s.ITLSamplesMS)
	}

	// 之后每次再出现内容，都应追加一条与上一次的间隔
	ApplySSEEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "b"}}}}, 130, s)
	ApplySSEEvent(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "c"}}}}, 180, s)

	want := []float64{30, 50}
	if len(s.ITLSamplesMS) != len(want) {
		t.Fatalf("ITLSamplesMS = %v, want %v", s.ITLSamplesMS, want)
	}
	for i, v := range want {
		if s.ITLSamplesMS[i] != v {
			t.Errorf("ITLSamplesMS[%d] = %v, want %v", i, s.ITLSamplesMS[i], v)
		}
	}
}

func TestApplySSEEvent_NoContentEventsNoITL(t *testing.T) {
	s := NewStreamSummary()
	// usage-only 事件，无输出内容：不应产生 ITL 样本，TTFT 也不应被打点
	ApplySSEEvent(map[string]any{"usage": map[string]any{"completion_tokens": float64(10)}}, 50, s)
	if s.TTFTMS >= 0 {
		t.Errorf("TTFTMS = %v, want < 0（未出现输出内容不应打 TTFT）", s.TTFTMS)
	}
	if len(s.ITLSamplesMS) != 0 {
		t.Errorf("ITLSamplesMS = %v, want empty", s.ITLSamplesMS)
	}
}
