package errlog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initTemp 把错误日志指向临时文件并返回读取全部记录的函数。
func initTemp(t *testing.T) func() []Entry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "request_errors.jsonl")
	Init(p)
	t.Cleanup(func() { Init("") })
	return func() []Entry {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("读取错误日志失败: %v", err)
		}
		var entries []Entry
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("解析记录失败: %v (%s)", err, line)
			}
			entries = append(entries, e)
		}
		return entries
	}
}

func TestExtractRequestIDs(t *testing.T) {
	h := http.Header{}
	h.Set("X-Request-Id", "rid-1")
	h.Set("X-WS-Request-Id", "ws-1")
	h.Set("X-Maas-Request-Id", "maas-1")
	h.Set("X-Oneapi-Request-Id", "oneapi-1")
	h.Set("X-Amzn-RequestId", "amzn-1")
	h.Set("CF-Ray", "ray-1")
	h.Set("Traceparent", "00-abc-def-01")
	h.Set("Content-Type", "application/json")

	body := []byte(`{"error":{"message":"boom","request_id":"body-rid"},"trace_id":"body-tid","data":[{"logId":"body-lid"}],"n":1}`)

	ids := ExtractRequestIDs(h, body)
	want := map[string]string{
		"header.x-request-id":        "rid-1",
		"header.x-ws-request-id":     "ws-1",
		"header.x-maas-request-id":   "maas-1",
		"header.x-oneapi-request-id": "oneapi-1",
		"header.x-amzn-requestid":    "amzn-1",
		"header.cf-ray":              "ray-1",
		"header.traceparent":         "00-abc-def-01",
		"body.request_id":            "body-rid",
		"body.trace_id":              "body-tid",
		"body.logId":                 "body-lid",
	}
	for k, v := range want {
		if ids[k] != v {
			t.Errorf("ids[%q] = %q, want %q", k, ids[k], v)
		}
	}
	if _, ok := ids["header.content-type"]; ok {
		t.Error("Content-Type 不应被当作请求 ID")
	}
	if len(ids) != len(want) {
		t.Errorf("提取到 %d 个 ID，期望 %d 个: %v", len(ids), len(want), ids)
	}
}

func TestExtractRequestIDsNonJSONBody(t *testing.T) {
	if ids := ExtractRequestIDs(http.Header{}, []byte("<html>502</html>")); ids != nil {
		t.Errorf("非 JSON 响应体应返回 nil，得到 %v", ids)
	}
}

func TestPrimaryRequestID(t *testing.T) {
	tests := []struct {
		name string
		ids  map[string]string
		want string
	}{
		{"空", nil, ""},
		{"已知头优先", map[string]string{
			"header.cf-ray":       "ray",
			"header.x-request-id": "rid",
			"body.request_id":     "brid",
		}, "rid"},
		{"用户网关头", map[string]string{
			"header.x-ws-request-id": "ws",
			"body.request_id":        "brid",
		}, "ws"},
		{"其余 header 兜底", map[string]string{
			"header.x-custom-trace-id": "ct",
			"body.request_id":          "brid",
		}, "ct"},
		{"body request 优先于其他 body", map[string]string{
			"body.trace_id":   "tid",
			"body.request_id": "brid",
		}, "brid"},
		{"仅剩 body 其他 ID", map[string]string{"body.trace_id": "tid"}, "tid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrimaryRequestID(tt.ids); got != tt.want {
				t.Errorf("PrimaryRequestID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("https://user:pass@gw.example.com/v1beta/models/g:streamGenerateContent?alt=sse&key=sk-secret&api_key=also-secret")
	got := RedactURL(u)
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "also-secret") || strings.Contains(got, "user:pass") {
		t.Errorf("RedactURL 泄露了凭据: %s", got)
	}
	if !strings.Contains(got, "alt=sse") {
		t.Errorf("RedactURL 丢失了普通参数: %s", got)
	}
}

func TestBodyForLogTruncation(t *testing.T) {
	defer SetRequestBodyLimit(defaultRequestBodyLimit)

	SetRequestBodyLimit(4)
	s, truncated, size := BodyForLog([]byte("123456"))
	if s != "1234" || !truncated || size != 6 {
		t.Errorf("截断结果 = (%q, %v, %d), want (\"1234\", true, 6)", s, truncated, size)
	}

	SetRequestBodyLimit(0) // 关闭截断
	s, truncated, size = BodyForLog([]byte("123456"))
	if s != "123456" || truncated || size != 6 {
		t.Errorf("关闭截断后 = (%q, %v, %d), want (\"123456\", false, 6)", s, truncated, size)
	}
}

func TestRecordNoInitNoop(t *testing.T) {
	Init("")
	Record(Entry{Stage: StageTransport, Error: "x"})
	if Count() != 0 {
		t.Errorf("未 Init 时 Count() = %d, want 0", Count())
	}
}

func TestTransportHTTPStatus(t *testing.T) {
	read := initTemp(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "rid-429")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited","request_id":"body-429"}}`))
	}))
	defer srv.Close()

	hc := &http.Client{Transport: WrapTransport(nil)}
	resp, err := hc.Post(srv.URL+"/v1/chat/completions?key=sk-secret", "application/json",
		bytes.NewReader([]byte(`{"model":"m1"}`)))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// 下游仍能读到完整响应体
	if !strings.Contains(string(body), "rate limited") {
		t.Errorf("响应体被吞掉了: %s", body)
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Stage != StageHTTPStatus || e.Status != 429 {
		t.Errorf("stage/status = %s/%d, want http_status/429", e.Stage, e.Status)
	}
	if e.RequestBody != `{"model":"m1"}` {
		t.Errorf("request_body = %q", e.RequestBody)
	}
	if strings.Contains(e.URL, "sk-secret") {
		t.Errorf("URL 未脱敏: %s", e.URL)
	}
	if e.RequestIDs["header.x-request-id"] != "rid-429" || e.RequestIDs["body.request_id"] != "body-429" {
		t.Errorf("request_ids = %v", e.RequestIDs)
	}
	if len(e.ResponseHeaders["X-Request-Id"]) == 0 {
		t.Errorf("response_headers 缺失: %v", e.ResponseHeaders)
	}
	if Count() != 1 {
		t.Errorf("Count() = %d, want 1", Count())
	}
}

func TestTransportError(t *testing.T) {
	read := initTemp(t)

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // 立即关掉：连接被拒绝

	hc := &http.Client{Transport: WrapTransport(nil)}
	_, err := hc.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{}`)))
	if err == nil {
		t.Fatal("期望连接失败")
	}

	entries := read()
	if len(entries) != 1 || entries[0].Stage != StageTransport || entries[0].Error == "" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTransportStreamBroken(t *testing.T) {
	read := initTemp(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Request-Id", "rid-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"x\":1}\n"))
		w.(http.Flusher).Flush()
		// 直接掐断连接，模拟流中途断开
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	hc := &http.Client{Transport: WrapTransport(nil)}
	resp, err := hc.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{"stream":true}`)))
	if err != nil {
		t.Fatalf("建流失败: %v", err)
	}
	_, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr == nil {
		t.Fatal("期望读流中断")
	}

	entries := read()
	if len(entries) != 1 {
		t.Fatalf("记录数 = %d, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Stage != StageStream || e.Status != 200 {
		t.Errorf("stage/status = %s/%d, want stream/200", e.Stage, e.Status)
	}
	if e.RequestIDs["header.x-request-id"] != "rid-stream" {
		t.Errorf("request_ids = %v", e.RequestIDs)
	}
}

func TestTransportSuppress(t *testing.T) {
	read := initTemp(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: WrapTransport(nil)}
	req, _ := http.NewRequestWithContext(Suppress(context.Background()), http.MethodPost, srv.URL,
		bytes.NewReader([]byte(`{}`)))
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	_ = resp.Body.Close()

	if entries := read(); len(entries) != 0 {
		t.Errorf("Suppress 后仍记录了 %d 条: %+v", len(entries), entries)
	}
}

func TestTransportSuccessNotLogged(t *testing.T) {
	read := initTemp(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	hc := &http.Client{Transport: WrapTransport(nil)}
	resp, err := hc.Post(srv.URL, "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if _, err = io.ReadAll(resp.Body); err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	_ = resp.Body.Close()

	if entries := read(); len(entries) != 0 {
		t.Errorf("成功请求不应记录，得到 %+v", entries)
	}
}
