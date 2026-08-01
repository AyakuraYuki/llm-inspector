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

	"github.com/openai/openai-go"
)

// Message 是一条对话消息。
type Message struct {
	Role    string `json:"role" yaml:"role"`
	Content string `json:"content" yaml:"content"`
}

// Tool 描述一个可供模型调用的函数。
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall 是模型返回的一次工具调用。
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Request 是一次聊天补全请求（协议无关）。
type Request struct {
	Model       string // 为空则用 Provider 默认模型
	Messages    []Message
	MaxTokens   int
	Temperature *float64
	JSONMode    bool
	Tools       []Tool
	// ToolsChoice 工具调用策略：""/"auto" 由模型决定；"any"/"required" 强制调用一次。
	ToolsChoice string
}

// Result 是一次调用的统一结果，流式与非流式共用。
// FinishReason 统一映射为 OpenAI 风格：stop / length / tool_calls。
type Result struct {
	Content          string
	FinishReason     string
	ToolCalls        []ToolCall
	PromptTokens     int64
	CompletionTokens int64
	Chunks           int     // 流式时的 SSE 事件数
	TTFTMS           float64 // 流式时为首 token 延迟
	LatencyMS        float64
}

// Provider 是模型服务客户端的统一接口。
type Provider interface {
	// Chat 发起非流式调用。
	Chat(ctx context.Context, req *Request) (*Result, error)
	// Stream 发起流式调用，记录 TTFT 并聚合全部增量内容。
	Stream(ctx context.Context, req *Request) (*Result, error)
	// Models 返回模型 id 列表。
	Models(ctx context.Context) ([]string, error)
	// Model 返回默认模型名。
	Model() string
	// Protocol 返回协议标识：openai / anthropic / gemini。
	Protocol() string
}

// HTTPError 是手写客户端的 HTTP 层错误。
type HTTPError struct {
	StatusCode int
	Body       string
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
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

// --- 手写客户端共用的 HTTP / SSE 辅助 ---

const maxErrorBody = 4096

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
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
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
	defer resp.Body.Close()
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
			return err
		}
	}
	return sc.Err()
}
