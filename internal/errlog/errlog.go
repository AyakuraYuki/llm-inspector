// Package errlog 把测试过程中遇到的请求错误落盘为 JSONL 文件，每行一条记录，
// 内容包括请求地址、原始请求体、响应 Header 以及从 Header/响应体中提取的
// RequestID（或与之相似的各种 ID），用于事后向上游/网关排障时提供凭据。
//
// 使用方式：
//   - 进程启动时调用 Init 指定输出文件；未 Init 时所有记录都是空操作。
//   - HTTP 客户端接入 WrapTransport 后，传输层错误、非 2xx 响应、
//     2xx 建流后读 body 中断三类错误会被自动记录；
//   - 对于 HTTP 200 之后才能判定的语义失败（流被无声截断、流内错误事件等），
//     调用方在拿得到响应上下文的位置手动 Record；
//   - 故意构造的失败请求（如评测工具的错误 key 探测、边界畸形载荷）用
//     Suppress 包装 context 跳过记录，避免淹没真正的错误。
package errlog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Stage 标识错误发生的阶段。
const (
	StageTransport  = "transport"   // 传输层失败（DNS/超时/连接被拒/TLS 等），没有响应
	StageHTTPStatus = "http_status" // 收到了非 2xx 的 HTTP 响应
	StageStream     = "stream"      // HTTP 2xx 建流之后才失败（读中断、流内错误事件、无声截断等）
)

// Entry 是一条请求错误记录，序列化为 JSONL 中的一行。
type Entry struct {
	Time                 string              `json:"time"`
	Stage                string              `json:"stage"`
	Method               string              `json:"method,omitempty"`
	URL                  string              `json:"url,omitempty"`
	Status               int                 `json:"status,omitempty"`
	RequestBody          string              `json:"request_body,omitempty"`
	RequestBodyTruncated bool                `json:"request_body_truncated,omitempty"`
	RequestBodySize      int                 `json:"request_body_size,omitempty"` // 原始请求体完整字节数
	ResponseHeaders      map[string][]string `json:"response_headers,omitempty"`
	RequestIDs           map[string]string   `json:"request_ids,omitempty"`
	ResponseBodySnippet  string              `json:"response_body_snippet,omitempty"`
	Error                string              `json:"error,omitempty"`
}

const (
	// defaultRequestBodyLimit 是记录原始请求体的默认截断上限。
	defaultRequestBodyLimit = 128 * 1024
	// maxResponseSnippet 是错误响应体摘要的截断上限。
	maxResponseSnippet = 8 * 1024
	// FullBodyEnv 置为 1/true 时关闭请求体截断，完整记录原始请求体。
	FullBodyEnv = "LLM_INSPECTOR_ERRLOG_FULL_BODY"
)

var (
	mu        sync.Mutex
	path      string
	file      *os.File
	count     int64
	bodyLimit = defaultRequestBodyLimit
)

func init() {
	if v := os.Getenv(FullBodyEnv); v == "1" || strings.EqualFold(v, "true") {
		bodyLimit = 0
	}
}

// Init 指定错误日志的输出文件并重置计数。文件与父目录都在首次记录时才创建，
// 一次运行没有任何错误时不会留下空文件。
func Init(p string) {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
		file = nil
	}
	path = p
	count = 0
}

// Path 返回 Init 设置的输出文件路径。
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Count 返回本次运行已记录的错误条数。
func Count() int64 {
	mu.Lock()
	defer mu.Unlock()
	return count
}

// SetRequestBodyLimit 设置记录原始请求体的截断上限（字节）；n <= 0 关闭截断。
func SetRequestBodyLimit(n int) {
	mu.Lock()
	defer mu.Unlock()
	bodyLimit = n
}

// Record 追加一条错误记录。未 Init 时为空操作。Time 为空时自动补当前时间。
func Record(e Entry) {
	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return
	}
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339Nano)
	}
	if file == nil {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "errlog: 无法创建目录 %s: %v\n", dir, err)
				return
			}
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "errlog: 无法打开错误日志 %s: %v\n", path, err)
			return
		}
		file = f
	}
	line, err := json.Marshal(e)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "errlog: 序列化错误记录失败: %v\n", err)
		return
	}
	if _, err = file.Write(append(line, '\n')); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "errlog: 写错误日志 %s 失败: %v\n", path, err)
		return
	}
	count++
}

type suppressKey struct{}

// Suppress 返回带「不记录错误」标记的 context。用于故意构造的失败请求
// （错误 key 探测、边界畸形载荷等），这些请求的 4xx 是期望结果。
func Suppress(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressKey{}, true)
}

// Suppressed 判断 ctx 是否带有 Suppress 标记。
func Suppressed(ctx context.Context) bool {
	v, _ := ctx.Value(suppressKey{}).(bool)
	return v
}

// BodyForLog 按截断上限整理请求体用于记录，返回（记录内容, 是否截断, 原始长度）。
func BodyForLog(b []byte) (string, bool, int) {
	mu.Lock()
	limit := bodyLimit
	mu.Unlock()
	if limit > 0 && len(b) > limit {
		return string(b[:limit]), true, len(b)
	}
	return string(b), false, len(b)
}

// --- RequestID 提取 ---

// requestIDKeyRe 匹配「像请求 ID」的键名：request_id / requestId / X-Request-Id /
// x-amzn-requestid / trace_id / log_id / correlation-id 等各种变体。
var requestIDKeyRe = regexp.MustCompile(`(?i)(request|trace|correlation|log)[-_]?id$`)

// extraIDHeaders 是不符合上述命名规律、但同样承载请求标识的响应头。
var extraIDHeaders = map[string]bool{
	"cf-ray":      true,
	"traceparent": true,
}

// primaryHeaderOrder 定义 PrimaryRequestID 挑选代表性 ID 的优先级。
// 前四个是已知网关（含 X-WS-Request-Id / X-Maas-Request-Id / X-Oneapi-Request-Id）
// 实际使用的请求 ID 头。
var primaryHeaderOrder = []string{
	"x-request-id",
	"x-ws-request-id",
	"x-maas-request-id",
	"x-oneapi-request-id",
	"request-id",
	"x-amzn-requestid",
	"x-ms-request-id",
	"cf-ray",
}

// ExtractRequestIDs 从响应 Header 与响应体（JSON，可为 nil）中提取全部请求 ID
// 类字段，键带 "header." / "body." 前缀标明来源，Header 键名统一小写。
func ExtractRequestIDs(h http.Header, body []byte) map[string]string {
	ids := map[string]string{}
	for k := range h {
		lk := strings.ToLower(k)
		if requestIDKeyRe.MatchString(lk) || extraIDHeaders[lk] {
			if v := h.Get(k); v != "" {
				ids["header."+lk] = v
			}
		}
	}
	if len(body) > 0 {
		var v any
		if err := json.Unmarshal(bytes.TrimSpace(body), &v); err == nil {
			collectBodyIDs(v, ids, 0)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// collectBodyIDs 递归扫描 JSON 值，收集键名匹配 requestIDKeyRe 的字符串/数字字段。
// 同名键只保留最先遇到的值（外层优先）。
func collectBodyIDs(v any, ids map[string]string, depth int) {
	if depth > 6 {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if requestIDKeyRe.MatchString(k) {
				key := "body." + k
				if _, exists := ids[key]; !exists {
					switch s := val.(type) {
					case string:
						if s != "" {
							ids[key] = s
						}
					case float64:
						ids[key] = fmt.Sprintf("%.0f", s)
					}
				}
			}
		}
		for _, val := range t {
			collectBodyIDs(val, ids, depth+1)
		}
	case []any:
		for i, val := range t {
			if i >= 8 {
				break
			}
			collectBodyIDs(val, ids, depth+1)
		}
	}
}

// PrimaryRequestID 从 ExtractRequestIDs 的结果中挑一个代表性 ID：
// 已知请求 ID 头优先，其次其余 Header 命中项，再次响应体中带 request 字样的键，
// 最后任取其余命中项（同级按键名排序保证确定性）。
func PrimaryRequestID(ids map[string]string) string {
	if len(ids) == 0 {
		return ""
	}
	for _, k := range primaryHeaderOrder {
		if v := ids["header."+k]; v != "" {
			return v
		}
	}
	pick := func(match func(string) bool) string {
		keys := make([]string, 0, len(ids))
		for k := range ids {
			if match(k) {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			return ""
		}
		sort.Strings(keys)
		return ids[keys[0]]
	}
	if v := pick(func(k string) bool { return strings.HasPrefix(k, "header.") }); v != "" {
		return v
	}
	if v := pick(func(k string) bool {
		return strings.HasPrefix(k, "body.") && strings.Contains(strings.ToLower(k), "request")
	}); v != "" {
		return v
	}
	return pick(func(string) bool { return true })
}

// --- URL 脱敏 ---

// sensitiveQueryParams 是 URL query 中需要脱敏的凭据参数名（统一小写比较）。
var sensitiveQueryParams = map[string]bool{
	"key":          true,
	"api_key":      true,
	"api-key":      true,
	"apikey":       true,
	"token":        true,
	"access_token": true,
}

// RedactURL 返回脱敏后的 URL 字符串：抹去 userinfo，凭据类 query 参数值替换为 ***。
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	c := *u
	c.User = nil
	if c.RawQuery != "" {
		q := c.Query()
		changed := false
		for k := range q {
			if sensitiveQueryParams[strings.ToLower(k)] {
				q.Set(k, "***")
				changed = true
			}
		}
		if changed {
			c.RawQuery = q.Encode()
		}
	}
	return c.String()
}

// RedactURLString 是 RedactURL 的字符串版本；解析失败时原样返回。
func RedactURLString(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	return RedactURL(u)
}

// --- Transport 包装器 ---

// WrapTransport 包装 base（nil 时用 http.DefaultTransport），自动记录：
//   - 传输层错误（StageTransport）；
//   - 非 2xx 响应（StageHTTPStatus），读取响应体前 maxResponseSnippet 字节
//     提取 RequestID 后原样还给下游；
//   - 2xx 建流之后 body 读取中断（StageStream，如超时、连接被重置）。
//
// 带 Suppress 标记的请求跳过全部记录。
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &roundTripper{base: base}
}

type roundTripper struct {
	base http.RoundTripper
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.base.RoundTrip(req)
	if Suppressed(req.Context()) {
		return resp, err
	}
	if err != nil {
		e := newEntry(StageTransport, req)
		e.Error = err.Error()
		Record(e)
		return resp, err
	}
	if resp.StatusCode >= 400 {
		snippet := peekBody(resp, maxResponseSnippet)
		e := newEntry(StageHTTPStatus, req)
		e.Status = resp.StatusCode
		e.ResponseHeaders = resp.Header
		e.RequestIDs = ExtractRequestIDs(resp.Header, snippet)
		e.ResponseBodySnippet = string(snippet)
		e.Error = "HTTP " + resp.Status
		Record(e)
		return resp, nil
	}
	// 2xx：包装 body 监听流中途的读取错误
	resp.Body = &watchBody{rc: resp.Body, req: req, resp: resp}
	return resp, nil
}

// newEntry 构造带请求上下文（方法、脱敏 URL、原始请求体）的记录。
func newEntry(stage string, req *http.Request) Entry {
	e := Entry{
		Stage:  stage,
		Method: req.Method,
		URL:    RedactURL(req.URL),
	}
	e.RequestBody, e.RequestBodyTruncated, e.RequestBodySize = captureRequestBody(req)
	return e
}

// captureRequestBody 通过 req.GetBody 重放请求体用于记录。请求体已被发送，
// 只有 GetBody 可用时才能取到；标准库对 bytes/strings Reader 会自动设置 GetBody。
func captureRequestBody(req *http.Request) (string, bool, int) {
	if req.GetBody == nil {
		return "", false, 0
	}
	rc, err := req.GetBody()
	if err != nil {
		return "", false, 0
	}
	defer func() { _ = rc.Close() }()

	mu.Lock()
	limit := bodyLimit
	mu.Unlock()

	var r io.Reader = rc
	if limit > 0 {
		r = io.LimitReader(rc, int64(limit))
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return string(b), false, len(b)
	}
	size := len(b)
	truncated := false
	if limit > 0 && req.ContentLength > int64(limit) {
		size = int(req.ContentLength)
		truncated = true
	}
	return string(b), truncated, size
}

// peekBody 读取响应体前最多 n 字节，并把读到的内容原样拼回 resp.Body，
// 不影响下游继续消费。
func peekBody(resp *http.Response, n int) []byte {
	if resp.Body == nil {
		return nil
	}
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, int64(n)))
	resp.Body = &prefixedBody{Reader: io.MultiReader(bytes.NewReader(buf), resp.Body), Closer: resp.Body}
	return buf
}

type prefixedBody struct {
	io.Reader
	io.Closer
}

// watchBody 包装 2xx 响应体，在读取中途出错（非 EOF）时记录一条 StageStream。
type watchBody struct {
	rc   io.ReadCloser
	req  *http.Request
	resp *http.Response
	once sync.Once
}

func (w *watchBody) Read(p []byte) (int, error) {
	n, err := w.rc.Read(p)
	if err != nil && err != io.EOF {
		w.once.Do(func() {
			e := newEntry(StageStream, w.req)
			e.Status = w.resp.StatusCode
			e.ResponseHeaders = w.resp.Header
			e.RequestIDs = ExtractRequestIDs(w.resp.Header, nil)
			e.Error = "读取响应流中断: " + err.Error()
			Record(e)
		})
	}
	return n, err
}

func (w *watchBody) Close() error { return w.rc.Close() }
