package provider

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
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
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

// TestStreamErrorEventLogged 验证 HTTP 200 建流后收到流内 error 事件时，
// ssePost 会带着响应上下文写入请求错误日志。
func TestStreamErrorEventLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Maas-Request-Id", "maas-err")
		f := w.(http.Flusher)
		_, _ = fmt.Fprint(w, `data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"},"request_id":"body-err"}`+"\n\n")
		f.Flush()
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL, "test-key", "test-model", time.Minute)
	_, err := p.Stream(context.Background(), &params.Request{
		Messages: []params.Message{{Role: "user", Content: "ping"}},
	})
	if err == nil {
		t.Fatal("期望流内错误事件返回 error")
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Stage != errlog.StageStream || e.Status != http.StatusOK {
		t.Errorf("stage/status = %s/%d, want stream/200", e.Stage, e.Status)
	}
	if !strings.Contains(e.RequestBody, `"test-model"`) {
		t.Errorf("request_body 缺少原始请求参数: %q", e.RequestBody)
	}
	if e.RequestIDs["header.x-maas-request-id"] != "maas-err" || e.RequestIDs["body.request_id"] != "body-err" {
		t.Errorf("request_ids = %v", e.RequestIDs)
	}
}

// TestRawChatNotLogged 验证边界测试用的裸请求（故意发送畸形负载）不写入错误日志。
func TestRawChatNotLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid"}}`))
	}))
	defer srv.Close()

	p := NewOpenAI(srv.URL, "test-key", "test-model", time.Minute)
	res, err := p.(RawCaller).RawChat(context.Background(), &RawRequest{
		Payload: map[string]any{"messages": "not-an-array"},
	})
	if err != nil {
		t.Fatalf("RawChat 网络层不应报错: %v", err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", res.StatusCode)
	}

	if entries := read(); len(entries) != 0 {
		t.Errorf("裸请求的 4xx 不应记录，得到 %+v", entries)
	}
}

// TestProviderHTTPStatusLogged 验证常规调用的 4xx/5xx 由 Transport 自动记录。
func TestProviderHTTPStatusLogged(t *testing.T) {
	read := initErrlog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Oneapi-Request-Id", "oneapi-500")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
	}))
	defer srv.Close()

	p := NewAnthropic(srv.URL, "test-key", "test-model", time.Minute)
	_, err := p.Chat(context.Background(), &params.Request{
		Messages: []params.Message{{Role: "user", Content: "ping"}},
	})
	if err == nil {
		t.Fatal("期望 5xx 返回 error")
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Stage != errlog.StageHTTPStatus || e.Status != http.StatusInternalServerError {
		t.Errorf("stage/status = %s/%d, want http_status/500", e.Stage, e.Status)
	}
	if e.RequestIDs["header.x-oneapi-request-id"] != "oneapi-500" {
		t.Errorf("request_ids = %v", e.RequestIDs)
	}
}
