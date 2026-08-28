// Package provider 定义统一的模型服务调用抽象，
// 支持 OpenAI 兼容、Anthropic 与 Gemini 三种协议。
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// Provider 是模型服务客户端的统一接口。
type Provider interface {
	// Chat 发起非流式调用。
	Chat(ctx context.Context, req *params.Request) (*params.Result, error)
	// Stream 发起流式调用，记录 TTFT 并聚合全部增量内容。
	Stream(ctx context.Context, req *params.Request) (*params.Result, error)
	// Models 返回模型 id 列表。
	Models(ctx context.Context) ([]string, error)
	// Model 返回默认模型名。
	Model() string
	// Protocol 返回协议标识：openai / anthropic / gemini。
	Protocol() string
}

// New 按 target.protocol 构造对应协议的客户端（缺省 openai）。
func New(t config.TargetConfig) (Provider, error) {
	timeout, err := t.TimeoutDuration()
	if err != nil {
		return nil, err
	}
	switch t.ProtocolNormalized() {
	case "openai":
		return NewOpenAI(t.BaseURL, t.APIKey, t.Model, timeout), nil
	case "anthropic":
		return NewAnthropic(t.BaseURL, t.APIKey, t.Model, timeout), nil
	case "gemini":
		return NewGemini(t.BaseURL, t.APIKey, t.Model, timeout), nil
	default:
		return nil, fmt.Errorf("未知协议 %q", t.Protocol)
	}
}

// RawCaller 支持向 chat 端点发送裸请求的可选能力。
// 三个内建 provider 均实现；边界测试（L6）通过类型断言获取。
type RawCaller interface {
	// RawChat 向本协议的 chat 端点 POST payload，返回原始状态码与响应体。
	// 网络层错误返回 error；HTTP 层任何状态码（含 4xx/5xx）都不算 error。
	RawChat(ctx context.Context, req *RawRequest) (*RawResult, error)
}

// RawRequest 是一次"裸请求"：payload 原样序列化为请求体，
// 绕过 SDK 的强类型校验，用于发送非法/畸形负载做边界测试。
type RawRequest struct {
	// Payload 请求体，原样 JSON 序列化（可含任意非法字段/类型）。
	Payload map[string]any
	// OverrideAuth 非空时替换默认鉴权凭据（OmitAuth 优先）。
	OverrideAuth string
	// OmitAuth 为 true 时不携带任何鉴权头。
	OmitAuth bool
}

// RawResult 是裸请求的原始响应。
type RawResult struct {
	Body       string // 截断至 maxErrorBody
	StatusCode int
}

// HTTPError 是手写客户端的 HTTP 层错误。
type HTTPError struct {
	Body       string
	StatusCode int
}

func (e *HTTPError) Error() string {
	body := e.Body
	if r := []rune(body); len(r) > 200 {
		body = string(r[:200]) + "…"
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, body)
}

// StatusCode 从错误中提取 HTTP 状态码；非 HTTP 错误返回 0。
func StatusCode(err error) int {
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		return apiErr.StatusCode
	}
	if httpErr, ok := errors.AsType[*HTTPError](err); ok {
		return httpErr.StatusCode
	}
	return 0
}

// --- 手写客户端共用的 HTTP / SSE 辅助 ---

const maxErrorBody = 4096

// newHTTPClient 构造带请求错误记录的 HTTP 客户端：传输层失败、非 2xx 响应和
// 2xx 建流后读中断都会自动写入 internal/errlog 的请求错误日志。
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: errlog.WrapTransport(nil)}
}

// doJSON 发送 JSON 请求并解码响应；状态码 >=400 时返回 *HTTPError。
func doJSON(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// rawPost 原样 POST payload 到 url，返回原始状态码与响应体（不视 4xx/5xx 为 error）。
// headers 已由调用方按 RawRequest 的鉴权语义构造完毕。
// 裸请求只用于边界测试（故意发送非法/畸形负载），4xx 是期望结果，
// 不写入请求错误日志。
func rawPost(ctx context.Context, hc *http.Client, url string, headers map[string]string, payload map[string]any) (*RawResult, error) {
	ctx = errlog.Suppress(ctx)
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &RawResult{StatusCode: resp.StatusCode, Body: string(b)}, nil
}

// ssePost 发起 POST 并按 SSE 逐条回调 data 载荷；[DONE] 或 EOF 结束。
func ssePost(ctx context.Context, hc *http.Client, url string, headers map[string]string, body any, fn func(data []byte) error) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if err := fn([]byte(payload)); err != nil {
			// 流内错误事件（如 anthropic 的 error event）：HTTP 已 2xx，
			// Transport 层记录不到，在此带响应上下文补记一条请求错误。
			if !errlog.Suppressed(ctx) {
				e := errlog.Entry{
					Stage:               errlog.StageStream,
					Method:              http.MethodPost,
					URL:                 errlog.RedactURLString(url),
					Status:              resp.StatusCode,
					ResponseHeaders:     resp.Header,
					RequestIDs:          errlog.ExtractRequestIDs(resp.Header, []byte(payload)),
					ResponseBodySnippet: payload,
					Error:               err.Error(),
				}
				e.RequestBody, e.RequestBodyTruncated, e.RequestBodySize = errlog.BodyForLog(data)
				errlog.Record(e)
			}
			return err
		}
	}
	return sc.Err()
}

func milliSince(t time.Time) float64 {
	return float64(time.Since(t).Milliseconds())
}
