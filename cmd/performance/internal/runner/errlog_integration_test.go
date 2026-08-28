package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
)

// initErrlog 把请求错误日志指向临时文件，返回读取全部记录的函数。
func initErrlog(t *testing.T) func() []errlog.Entry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "request_errors.jsonl")
	errlog.Init(p)
	t.Cleanup(func() { errlog.Init("") })
	return func() []errlog.Entry {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("读取错误日志失败: %v", err)
		}
		var entries []errlog.Entry
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var e errlog.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("解析记录失败: %v (%s)", err, line)
			}
			entries = append(entries, e)
		}
		return entries
	}
}

func TestDoOpenAIRequest_HTTPStatusLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-WS-Request-Id", "ws-429")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","request_id":"body-429"}}`))
	}))
	defer srv.Close()

	cfg := types.BenchmarkConfig{BaseURL: srv.URL, Prompt: "hi"}
	model := types.ModelSpec{Name: "m1", Provider: types.ProviderOpenAI, Tokens: []string{"tk"}}
	m := doOpenAIRequest(context.Background(), cfg, model)

	if m.Success {
		t.Fatal("Success = true, want false")
	}
	if m.ErrorType != types.ErrorTypeRateLimit {
		t.Errorf("ErrorType = %s, want %s", m.ErrorType, types.ErrorTypeRateLimit)
	}
	if m.RequestID != "ws-429" {
		t.Errorf("RequestID = %q, want ws-429", m.RequestID)
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Stage != errlog.StageHTTPStatus || e.Status != http.StatusTooManyRequests {
		t.Errorf("stage/status = %s/%d, want http_status/429", e.Stage, e.Status)
	}
	if !strings.Contains(e.RequestBody, `"model":"m1"`) {
		t.Errorf("request_body 缺少原始请求参数: %q", e.RequestBody)
	}
	if e.RequestIDs["header.x-ws-request-id"] != "ws-429" || e.RequestIDs["body.request_id"] != "body-429" {
		t.Errorf("request_ids = %v", e.RequestIDs)
	}
	if len(e.ResponseHeaders["X-Ws-Request-Id"]) == 0 {
		t.Errorf("response_headers 缺失: %v", e.ResponseHeaders)
	}
}

func TestDoOpenAIRequest_UpstreamErrorLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "rid-up")
		f := w.(http.Flusher)
		_, _ = fmt.Fprint(w, `data: {"error":{"message":"boom"}}`+"\n\n")
		f.Flush()
	}))
	defer srv.Close()

	cfg := types.BenchmarkConfig{BaseURL: srv.URL, Prompt: "hi"}
	model := types.ModelSpec{Name: "m1", Provider: types.ProviderOpenAI, Tokens: []string{"tk"}}
	m := doOpenAIRequest(context.Background(), cfg, model)

	if m.Success || m.ErrorType != types.ErrorTypeUpstreamError {
		t.Fatalf("Success/ErrorType = %v/%s, want false/%s", m.Success, m.ErrorType, types.ErrorTypeUpstreamError)
	}
	if m.RequestID != "rid-up" {
		t.Errorf("RequestID = %q, want rid-up", m.RequestID)
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(entries))
	}
	if entries[0].Stage != errlog.StageStream || entries[0].Status != http.StatusOK {
		t.Errorf("stage/status = %s/%d, want stream/200", entries[0].Stage, entries[0].Status)
	}
}

func TestDoOpenAIRequest_CanceledNotLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟压测中止：在途请求批量取消

	cfg := types.BenchmarkConfig{BaseURL: srv.URL, Prompt: "hi"}
	model := types.ModelSpec{Name: "m1", Provider: types.ProviderOpenAI, Tokens: []string{"tk"}}
	m := doOpenAIRequest(ctx, cfg, model)

	if m.Success || m.ErrorType != types.ErrorTypeCanceled {
		t.Fatalf("Success/ErrorType = %v/%s, want false/%s", m.Success, m.ErrorType, types.ErrorTypeCanceled)
	}
	if entries := read(); len(entries) != 0 {
		t.Errorf("canceled 不应记录，得到 %+v", entries)
	}
}
