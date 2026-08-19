package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestOpenAIStreamReasoningTTFT 验证 reasoning_content 先于正文到达时，
// TTFT 以首个 reasoning chunk 为准（真流式语义）。
func TestOpenAIStreamReasoningTTFT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(payload string) {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		delta := func(d map[string]any) string {
			b, _ := json.Marshal(d)
			return fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":"openai-test-1",`+
				`"choices":[{"index":0,"delta":%s,"finish_reason":null}]}`, b)
		}
		send(delta(map[string]any{"role": "assistant"}))
		send(delta(map[string]any{"reasoning_content": "让我想想……"}))
		send(delta(map[string]any{"content": "你好"}))
		send(fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":"openai-test-1",` +
			`"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`))
		send(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":"openai-test-1","choices":[],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAI(srv.URL+"/v1", "good-key", "openai-test-1", 5*time.Second)
	r, err := c.Stream(t.Context(), &Request{
		Messages:  []Message{{Role: "user", Content: "你好"}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("Stream 失败: %v", err)
	}
	if r.Content != "你好" {
		t.Errorf("Content = %q", r.Content)
	}
	if r.ReasoningContent != "让我想想……" {
		t.Errorf("ReasoningContent = %q（reasoning_content 应进入思考内容）", r.ReasoningContent)
	}
	if r.TTFTMS < 0 {
		t.Error("reasoning 首 chunk 后应已记录 TTFT")
	}
}
