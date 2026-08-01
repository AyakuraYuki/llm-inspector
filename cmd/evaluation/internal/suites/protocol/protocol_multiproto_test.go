package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
)

// --- Anthropic L2 mock：支持 prompt 诱导 JSON 与 tool_use ---

func newAnthropicL2Server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools      []map[string]any `json:"tools"`
			ToolChoice *struct {
				Type string `json:"type"`
			} `json:"tool_choice"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")

		if len(req.Tools) > 0 {
			if req.ToolChoice == nil || req.ToolChoice.Type != "any" {
				t.Errorf("Anthropic 强制工具调用应传 tool_choice.type=any")
			}
			fmt.Fprint(w, `{"id":"m1","type":"message","role":"assistant",`+
				`"content":[{"type":"tool_use","id":"t1","name":"get_weather","input":{"city":"Paris"}}],`+
				`"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
			return
		}
		// prompt 诱导 JSON
		last := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(last, "JSON") {
			fmt.Fprint(w, `{"id":"m1","type":"message","role":"assistant",`+
				`"content":[{"type":"text","text":"{\"city\":\"Paris\"}"}],`+
				`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`)
			return
		}
		fmt.Fprint(w, `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],`+
			`"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	return httptest.NewServer(mux)
}

func TestAnthropicJSONModePromptInduced(t *testing.T) {
	srv := newAnthropicL2Server(t)
	defer srv.Close()
	p := provider.NewAnthropic(srv.URL, "k", "claude-test-1", 5*time.Second)

	r := checkJSONMode(t.Context(), p)
	if r.Status != core.StatusPass || r.Score != 1 {
		t.Fatalf("Anthropic prompt 诱导 JSON 应通过: status=%s score=%v detail=%s",
			r.Status, r.Score, r.Detail)
	}
	if !strings.Contains(r.Detail, "prompt 诱导") {
		t.Errorf("detail 应注明 prompt 诱导, got %q", r.Detail)
	}
}

func TestAnthropicToolCallingForced(t *testing.T) {
	srv := newAnthropicL2Server(t)
	defer srv.Close()
	p := provider.NewAnthropic(srv.URL, "k", "claude-test-1", 5*time.Second)

	r := checkToolCalling(t.Context(), p)
	if r.Status != core.StatusPass || r.Score != 1 {
		t.Fatalf("Anthropic tool_calling 应通过: status=%s score=%v detail=%s",
			r.Status, r.Score, r.Detail)
	}
}

// --- Gemini L2 mock：支持原生 JSON mode 与 functionCall ---

func newGeminiL2Server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			GenerationConfig *struct {
				ResponseMIMEType string `json:"responseMimeType"`
			} `json:"generationConfig"`
			Tools      []map[string]any `json:"tools"`
			ToolConfig *struct {
				FunctionCallingConfig struct {
					Mode string `json:"mode"`
				} `json:"functionCallingConfig"`
			} `json:"toolConfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		usage := `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}`

		if len(req.Tools) > 0 {
			if req.ToolConfig == nil || req.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
				t.Errorf("Gemini 强制工具调用应传 toolConfig mode=ANY")
			}
			fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],%s}`, usage)
			return
		}
		if req.GenerationConfig != nil && req.GenerationConfig.ResponseMIMEType == "application/json" {
			fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"city\":\"Paris\"}"}]},"finishReason":"STOP"}],%s}`, usage)
			return
		}
		fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],%s}`, usage)
	})
	return httptest.NewServer(mux)
}

func TestGeminiJSONModeNative(t *testing.T) {
	srv := newGeminiL2Server(t)
	defer srv.Close()
	p := provider.NewGemini(srv.URL, "k", "gemini-test-1", 5*time.Second)

	r := checkJSONMode(t.Context(), p)
	if r.Status != core.StatusPass || r.Score != 1 {
		t.Fatalf("Gemini 原生 JSON mode 应通过: status=%s score=%v detail=%s",
			r.Status, r.Score, r.Detail)
	}
}

func TestGeminiToolCallingForced(t *testing.T) {
	srv := newGeminiL2Server(t)
	defer srv.Close()
	p := provider.NewGemini(srv.URL, "k", "gemini-test-1", 5*time.Second)

	r := checkToolCalling(t.Context(), p)
	if r.Status != core.StatusPass || r.Score != 1 {
		t.Fatalf("Gemini tool_calling 应通过: status=%s score=%v detail=%s",
			r.Status, r.Score, r.Detail)
	}
}
