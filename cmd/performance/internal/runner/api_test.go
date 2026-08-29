package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// sseServer 返回一个输出固定 SSE 流的测试服务器，并把响应体交给 parseStreamMetrics。
// 金丝雀测试：锁定 parseStreamMetrics 现有行为，重构到共享包后必须原样通过。
func runStreamFixture(t *testing.T, writeStream func(w http.ResponseWriter, f http.Flusher)) types.RequestMetrics {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter 不支持 Flush")
			return
		}
		writeStream(w, f)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	t0 := time.Now()
	return parseStreamMetrics(t0, resp.Body)
}

func sendSSE(f http.Flusher, w http.ResponseWriter, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	f.Flush()
}

func TestParseStreamMetrics_OpenAISuccess(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		sendSSE(f, w, `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
		sendSSE(f, w, `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"思考"},"finish_reason":null}]}`)
		sendSSE(f, w, `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":null}]}`)
		sendSSE(f, w, `{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		sendSSE(f, w, `{"id":"c","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	})

	if !m.Success {
		t.Fatalf("Success = false, Error = %q, ErrorType = %s", m.Error, m.ErrorType)
	}
	if m.InputTokens != 12 || m.OutputTokens != 7 {
		t.Errorf("tokens = (%d, %d), want (12, 7)", m.InputTokens, m.OutputTokens)
	}
	if m.CacheReported {
		t.Errorf("CacheReported = true, want false（usage 未携带缓存字段）")
	}
	if m.TTFT <= 0 {
		t.Errorf("TTFT = %v, want > 0（reasoning 首 chunk 应计 TTFT）", m.TTFT)
	}
}

func TestParseStreamMetrics_AnthropicSuccess(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		sendSSE(f, w, `{"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":4}}}`)
		sendSSE(f, w, `{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"思考"}}`)
		sendSSE(f, w, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)
		sendSSE(f, w, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)
		sendSSE(f, w, `{"type":"message_stop"}`)
	})

	if !m.Success {
		t.Fatalf("Success = false, Error = %q, ErrorType = %s", m.Error, m.ErrorType)
	}
	if m.InputTokens != 14 || m.OutputTokens != 7 {
		t.Errorf("tokens = (%d, %d), want (14, 7)（InputTokens = input_tokens + cache_read_input_tokens）", m.InputTokens, m.OutputTokens)
	}
	if m.CachedInputTokens != 4 {
		t.Errorf("CachedInputTokens = %d, want 4", m.CachedInputTokens)
	}
	if !m.CacheReported {
		t.Errorf("CacheReported = false, want true（message_start 携带 cache_read_input_tokens）")
	}
	if m.TTFT <= 0 {
		t.Errorf("TTFT = %v, want > 0（thinking_delta 应计 TTFT）", m.TTFT)
	}
}

func TestParseStreamMetrics_GeminiSuccess(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		sendSSE(f, w, `{"candidates":[{"content":{"parts":[{"text":"你好"}]},"finishReason":null}]}`)
		sendSSE(f, w, `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":1}}`)
	})

	if !m.Success {
		t.Fatalf("Success = false, Error = %q, ErrorType = %s", m.Error, m.ErrorType)
	}
	// candidates + thoughts = 5
	if m.InputTokens != 10 || m.OutputTokens != 5 {
		t.Errorf("tokens = (%d, %d), want (10, 5)", m.InputTokens, m.OutputTokens)
	}
	if m.CachedInputTokens != 1 {
		t.Errorf("CachedInputTokens = %d, want 1", m.CachedInputTokens)
	}
	if !m.CacheReported {
		t.Errorf("CacheReported = false, want true（usageMetadata 携带 cachedContentTokenCount）")
	}
}

func TestParseStreamMetrics_ResponsesSuccess(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		sendSSE(f, w, `{"type":"response.output_text.delta","delta":"你好"}`)
		sendSSE(f, w, `{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":7,"input_tokens_details":{"cached_tokens":3}}}}`)
	})

	if !m.Success {
		t.Fatalf("Success = false, Error = %q, ErrorType = %s", m.Error, m.ErrorType)
	}
	if m.InputTokens != 10 || m.OutputTokens != 7 {
		t.Errorf("tokens = (%d, %d), want (10, 7)", m.InputTokens, m.OutputTokens)
	}
	if m.CachedInputTokens != 3 {
		t.Errorf("CachedInputTokens = %d, want 3", m.CachedInputTokens)
	}
	if !m.CacheReported {
		t.Errorf("CacheReported = false, want true（input_tokens_details 携带 cached_tokens）")
	}
}

func TestParseStreamMetrics_UpstreamError(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		sendSSE(f, w, `{"error":{"message":"boom"}}`)
	})

	if m.Success {
		t.Fatal("Success = true, want false")
	}
	if m.ErrorType != types.ErrorTypeUpstreamError {
		t.Errorf("ErrorType = %s, want %s", m.ErrorType, types.ErrorTypeUpstreamError)
	}
}

func TestParseStreamMetrics_NoContent(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		// 只有元数据事件与终止标记，无任何输出内容
		sendSSE(f, w, `{"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`)
		sendSSE(f, w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	})

	if m.Success {
		t.Fatal("Success = true, want false")
	}
	if m.ErrorType != types.ErrorTypeNoContent {
		t.Errorf("ErrorType = %s, want %s", m.ErrorType, types.ErrorTypeNoContent)
	}
}

func TestParseStreamMetrics_StreamTruncated(t *testing.T) {
	m := runStreamFixture(t, func(w http.ResponseWriter, f http.Flusher) {
		// 有内容但无终止标记，正常 EOF
		sendSSE(f, w, `{"choices":[{"delta":{"content":"你好"},"finish_reason":null}]}`)
	})

	if m.Success {
		t.Fatal("Success = true, want false")
	}
	if m.ErrorType != types.ErrorTypeStreamTruncated {
		t.Errorf("ErrorType = %s, want %s", m.ErrorType, types.ErrorTypeStreamTruncated)
	}
}

func TestParseStreamMetrics_StreamBroken(t *testing.T) {
	// 声明 Content-Length 后只写一半就断连：客户端读到 unexpected EOF，
	// scanner.Err() 非 nil → StreamBroken
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		body := []byte(`data: {"choices":[{"delta":{"content":"你好"},"finish_reason":null}]}` + "\n\n")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)+100))
		f, _ := w.(http.Flusher)
		_, _ = w.Write(body)
		f.Flush()
		// 直接关闭连接，不写剩余字节
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	m := parseStreamMetrics(time.Now(), resp.Body)
	if m.Success {
		t.Fatal("Success = true, want false")
	}
	if m.ErrorType != types.ErrorTypeStreamBroken {
		t.Errorf("ErrorType = %s, want %s", m.ErrorType, types.ErrorTypeStreamBroken)
	}
}
