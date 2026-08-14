package availability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// --- Anthropic mock（L1 最小集） ---

func newAnthropicL1Server(t *testing.T, badModelStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("x-api-key") != "good-key" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fmt.Fprint(w, `{"data":[{"type":"model","id":"claude-test-1"}],"has_more":false}`)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "nonexistent-model-00000000" {
			w.WriteHeader(badModelStatus)
			fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"no model"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"p"}],`+
			`"stop_reason":"max_tokens","usage":{"input_tokens":3,"output_tokens":1}}`)
	})
	return httptest.NewServer(mux)
}

// --- Gemini mock（L1 最小集） ---

func newGeminiL1Server(t *testing.T, badModelStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("x-goog-api-key") != "good-key" {
			w.WriteHeader(400) // Gemini 对无效 key 返回 400
			fmt.Fprint(w, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT"}}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/v1beta/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		fmt.Fprint(w, `{"models":[{"name":"models/gemini-test-1"}]}`)
	})
	mux.HandleFunc("/v1beta/models/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
		model, _, _ := strings.Cut(tail, ":")
		if model == "nonexistent-model-00000000" {
			w.WriteHeader(badModelStatus)
			fmt.Fprintf(w, `{"error":{"code":%d,"message":"not found","status":"NOT_FOUND"}}`, badModelStatus)
			return
		}
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"p"}]},"finishReason":"MAX_TOKENS"}],`+
			`"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`)
	})
	return httptest.NewServer(mux)
}

func TestRunAnthropicL1(t *testing.T) {
	srv := newAnthropicL1Server(t, 404)
	defer srv.Close()
	p := provider.NewAnthropic(srv.URL, "good-key", "claude-test-1", 5*time.Second)
	bad := provider.NewAnthropic(srv.URL, "sk-invalid-key-for-eval", "claude-test-1", 5*time.Second)

	layer := Run(t.Context(), p, bad)
	layer.Compute(0.8)
	if layer.HasFail() {
		for _, c := range layer.Checks {
			t.Logf("%s: %s score=%v detail=%s", c.Name, c.Status, c.Score, c.Detail)
		}
		t.Fatal("Anthropic L1 不应有 fail 项")
	}
	if !layer.Passed {
		t.Fatalf("Anthropic L1 应通过, score=%v", layer.Score)
	}
	// 四项检查应全满分（404 为标准语义）
	if layer.Score != 1 {
		t.Fatalf("Anthropic L1 应满分, score=%v", layer.Score)
	}
}

func TestRunGeminiL1(t *testing.T) {
	srv := newGeminiL1Server(t, 404)
	defer srv.Close()
	p := provider.NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)
	bad := provider.NewGemini(srv.URL, "sk-invalid-key-for-eval", "gemini-test-1", 5*time.Second)

	layer := Run(t.Context(), p, bad)
	layer.Compute(0.8)
	if layer.HasFail() {
		for _, c := range layer.Checks {
			t.Logf("%s: %s score=%v detail=%s", c.Name, c.Status, c.Score, c.Detail)
		}
		t.Fatal("Gemini L1 不应有 fail 项")
	}
	// Gemini 坏 key 返回 400，应计满分
	sem := findCheck(&layer, "error_semantics")
	if sem == nil || sem.Score != 1 {
		t.Fatalf("Gemini 错误语义应满分（400 为标准拒绝）, got %+v", sem)
	}
}

func TestRunGeminiL1BadModel503(t *testing.T) {
	srv := newGeminiL1Server(t, 503) // 网关式拒绝
	defer srv.Close()
	p := provider.NewGemini(srv.URL, "good-key", "gemini-test-1", 5*time.Second)
	bad := provider.NewGemini(srv.URL, "sk-invalid-key-for-eval", "gemini-test-1", 5*time.Second)

	layer := Run(t.Context(), p, bad)
	layer.Compute(0.8)
	sem := findCheck(&layer, "error_semantics")
	if sem == nil {
		t.Fatal("缺少 error_semantics")
	}
	// 坏 key 400（满分）+ 坏模型 503（半分）= 0.75，pass
	if sem.Status != types.StatusPass || sem.Score != 0.75 {
		t.Fatalf("503 应半分通过, status=%s score=%v", sem.Status, sem.Score)
	}
}

func findCheck(l *types.LayerResult, name string) *types.CheckResult {
	for i := range l.Checks {
		if l.Checks[i].Name == name {
			return &l.Checks[i]
		}
	}
	return nil
}
