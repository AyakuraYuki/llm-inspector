package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// newGeminiServer 构造 Gemini 协议 mock。
func newGeminiServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("x-goog-api-key") != "good-key" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`)
			return false
		}
		return true
	}

	mux.HandleFunc("/v1beta/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"models/gemini-test-1","displayName":"Test"}]}`)
	})

	mux.HandleFunc("/v1beta/models/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		// 路径形如 /v1beta/models/{model}:generateContent 或 :streamGenerateContent
		tail := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
		model, action, _ := strings.Cut(tail, ":")
		if model == "nonexistent-model-00000000" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"error":{"code":404,"message":"model not found","status":"NOT_FOUND"}}`)
			return
		}
		var req geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		usage := `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15,"cachedContentTokenCount":3}`

		// 工具调用（ANY 模式）
		if len(req.Tools) > 0 && req.ToolConfig != nil && req.ToolConfig.FunctionCallingConfig.Mode == "ANY" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],%s}`, usage)
			return
		}

		// 流式（首 chunk 带 thought part：思考内容应先于正文到达）
		if action == "streamGenerateContent" {
			send := func(w http.ResponseWriter, payload string) {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}

			w.Header().Set("Content-Type", "text/event-stream")
			send(w, `{"candidates": [{"content": {"role": "model", "parts": [{"text": "让我想想……", "thought": true}, {"text": "你"}]}}]}`)
			send(w, `{"candidates": [{"content": {"role": "model", "parts": [{"text": "好"}]}}]}`)
			send(w, fmt.Sprintf(`{"candidates": [{"content": {"role": "model", "parts": [{"text": "！"}]}, "finishReason": "STOP"}], %s}`, usage))
			return
		}

		// JSON mode
		if req.GenerationConfig != nil && req.GenerationConfig.ResponseMIMEType == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"candidates": [{"content": {"role":"model","parts":[{"text":"{\"city\":\"Paris\"}"}]},"finishReason":"STOP"}],%s}`, usage)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"candidates": [{"content": {"role": "model","parts":[{"text":"你好"}]},"finishReason":"STOP"}],%s}`, usage)
	})

	return httptest.NewServer(mux)
}

func TestGeminiChat(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	c := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)

	r, err := c.Chat(t.Context(), &params.Request{
		Messages: []params.Message{
			{Role: "system", Content: "系统指令"},
			{Role: "user", Content: "你好"},
		},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if r.Content != "你好" {
		t.Errorf("Content = %q", r.Content)
	}
	if r.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", r.FinishReason)
	}
	if r.PromptTokens != 10 || r.CompletionTokens != 5 {
		t.Errorf("usage = %d/%d", r.PromptTokens, r.CompletionTokens)
	}
	if r.CachedInputTokens != 3 {
		t.Errorf("CachedInputTokens = %d, want 3（cachedContentTokenCount）", r.CachedInputTokens)
	}
}

func TestGeminiJSONMode(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	c := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)

	r, err := c.Chat(t.Context(), &params.Request{
		Messages: []params.Message{{Role: "user", Content: "给 JSON"}},
		JSONMode: true,
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if !json.Valid([]byte(r.Content)) {
		t.Errorf("输出不是合法 JSON: %q", r.Content)
	}
}

func TestGeminiToolCall(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	c := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)

	r, err := c.Chat(t.Context(), &params.Request{
		Messages:    []params.Message{{Role: "user", Content: "查天气"}},
		Tools:       []params.Tool{{Name: "get_weather", Parameters: map[string]any{"type": "object"}}},
		ToolsChoice: "any",
	})
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v", r.ToolCalls)
	}
	if !json.Valid([]byte(r.ToolCalls[0].Arguments)) {
		t.Errorf("Arguments 不是合法 JSON: %q", r.ToolCalls[0].Arguments)
	}
}

func TestGeminiStream(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	c := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)

	r, err := c.Stream(t.Context(), &params.Request{Messages: []params.Message{{Role: "user", Content: "你好"}}})
	if err != nil {
		t.Fatalf("Stream 失败: %v", err)
	}
	if r.Content != "你好！" {
		t.Errorf("Content = %q", r.Content)
	}
	if r.TTFTMS < 0 {
		t.Error("未记录 TTFT")
	}
	if r.ReasoningContent != "让我想想……" {
		t.Errorf("ReasoningContent = %q（thought part 应进入思考内容）", r.ReasoningContent)
	}
	if r.Chunks != 3 {
		t.Errorf("Chunks = %d, want 3", r.Chunks)
	}
	if r.FinishReason != "stop" {
		t.Errorf("FinishReason = %q", r.FinishReason)
	}
	if r.PromptTokens != 10 || r.CompletionTokens != 5 {
		t.Errorf("usage = %d/%d", r.PromptTokens, r.CompletionTokens)
	}
}

func TestGeminiModels(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	c := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)

	models, err := c.Models(t.Context())
	if err != nil {
		t.Fatalf("Models 失败: %v", err)
	}
	if len(models) != 1 || models[0] != "gemini-test-1" {
		t.Errorf("models = %v（应去掉 models/ 前缀）", models)
	}
}

func TestGeminiErrorStatus(t *testing.T) {
	srv := newGeminiServer(t)
	defer srv.Close()
	bad := NewGemini(srv.URL, "wrong-key", "gemini-test-1", 5*time.Second)

	_, err := bad.Chat(t.Context(), &params.Request{Messages: []params.Message{{Role: "user", Content: "x"}}})
	if code := StatusCode(err); code != 400 {
		t.Errorf("坏 key StatusCode = %d, want 400（Gemini 标准拒绝）", code)
	}

	good := NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)
	_, err = good.Chat(t.Context(), &params.Request{
		Model:    "nonexistent-model-00000000",
		Messages: []params.Message{{Role: "user", Content: "x"}},
	})
	if code := StatusCode(err); code != 404 {
		t.Errorf("坏模型 StatusCode = %d, want 404", code)
	}
}
