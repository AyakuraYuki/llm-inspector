package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newAnthropicServer 构造 Anthropic 协议 mock。
func newAnthropicServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "good-key" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		if r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"missing anthropic-version"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-test-1","display_name":"Test","created_at":"2024-01-01T00:00:00Z"}],"has_more":false}`)
	})

	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "good-key" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
			return
		}
		var req anthropicRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "nonexistent-model-00000000" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"model not found"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		// 工具调用
		if len(req.Tools) > 0 && req.ToolChoice != nil && req.ToolChoice.Type == "any" {
			fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"`+req.Model+`",`+
				`"content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}],`+
				`"stop_reason":"tool_use","usage":{"input_tokens":20,"output_tokens":15}}`)
			return
		}

		// 流式
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
			fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
			fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你\"}}\n\n")
			fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"好\"}}\n\n")
			fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
			fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}

		// system 提取检查
		if req.System == "" && len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "system 应被提取") {
			fmt.Fprint(w, `{"error":"system not extracted"}`)
			return
		}

		fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":%q,`+
			`"content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":10,"output_tokens":2}}`, req.Model)
	})

	return httptest.NewServer(mux)
}

func TestAnthropicChat(t *testing.T) {
	srv := newAnthropicServer(t)
	defer srv.Close()
	c := NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)

	r, err := c.Chat(t.Context(), &Request{
		Messages: []Message{
			{Role: "system", Content: "system 应被提取"},
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
	if r.PromptTokens != 10 || r.CompletionTokens != 2 {
		t.Errorf("usage = %d/%d", r.PromptTokens, r.CompletionTokens)
	}
}

func TestAnthropicToolCall(t *testing.T) {
	srv := newAnthropicServer(t)
	defer srv.Close()
	c := NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)

	r, err := c.Chat(t.Context(), &Request{
		Messages:    []Message{{Role: "user", Content: "查天气"}},
		Tools:       []Tool{{Name: "get_weather", Description: "查天气", Parameters: map[string]any{"type": "object"}}},
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
	if r.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", r.FinishReason)
	}
}

func TestAnthropicStream(t *testing.T) {
	srv := newAnthropicServer(t)
	defer srv.Close()
	c := NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)

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
	if r.TTFTMS < 0 {
		t.Error("未记录 TTFT")
	}
	if r.Chunks < 4 {
		t.Errorf("Chunks = %d", r.Chunks)
	}
	if r.FinishReason != "stop" {
		t.Errorf("FinishReason = %q", r.FinishReason)
	}
	if r.PromptTokens != 10 || r.CompletionTokens != 2 {
		t.Errorf("usage = %d/%d", r.PromptTokens, r.CompletionTokens)
	}
}

func TestAnthropicModels(t *testing.T) {
	srv := newAnthropicServer(t)
	defer srv.Close()
	c := NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)

	models, err := c.Models(t.Context())
	if err != nil {
		t.Fatalf("Models 失败: %v", err)
	}
	if len(models) != 1 || models[0] != "claude-test-1" {
		t.Errorf("models = %v", models)
	}
}

func TestAnthropicErrorStatus(t *testing.T) {
	srv := newAnthropicServer(t)
	defer srv.Close()
	bad := NewAnthropic(srv.URL, "wrong-key", "claude-test-1", 5*time.Second)

	_, err := bad.Chat(t.Context(), &Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if code := StatusCode(err); code != 401 {
		t.Errorf("坏 key StatusCode = %d, want 401", code)
	}

	good := NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)
	_, err = good.Chat(t.Context(), &Request{
		Model:    "nonexistent-model-00000000",
		Messages: []Message{{Role: "user", Content: "x"}},
	})
	if code := StatusCode(err); code != 404 {
		t.Errorf("坏模型 StatusCode = %d, want 404", code)
	}
}
