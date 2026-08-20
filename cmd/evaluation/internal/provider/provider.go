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
)

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

// Message 是一条对话消息。
// role=assistant 时可携带 ToolCalls（工具调用回传场景）；
// role=tool 时 Content 为工具执行结果，ToolCallID 关联此前的调用，
// Name 为函数名（Gemini 的 functionResponse 需要函数名而非 ID）。
type Message struct {
	Role       string     `json:"role" yaml:"role"`
	Content    string     `json:"content" yaml:"content"`
	Name       string     `json:"name,omitempty" yaml:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`
}

// Tool 描述一个可供模型调用的函数。
type Tool struct {
	Parameters  map[string]any
	Name        string
	Description string
}

// ToolCall 是模型返回的一次工具调用。
// ID 用于把工具结果回传给模型（Gemini 无 ID 概念，用函数名代替）。
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// JSONSchemaSpec 描述 response_format 的 json_schema 模式。
// openai 走原生 response_format；gemini 走 generationConfig.responseSchema；
// anthropic 无原生支持，由检查项走 prompt 诱导。
type JSONSchemaSpec struct {
	Schema map[string]any
	Name   string
	Strict bool
}

// Request 是一次聊天补全请求（协议无关）。
// 指针类型的字段为 nil 时表示"不传该参数"，由服务端使用默认值。
type Request struct {
	Temperature         *float64        // 温度
	TopP                *float64        // top_p 核采样，[0.0, 1.0] 之间
	FrequencyPenalty    *float64        // FrequencyPenalty 频率惩罚，仅 openai 支持
	PresencePenalty     *float64        // PresencePenalty 存在惩罚，仅 openai 支持
	Seed                *int64          // Seed 采样种子。openai/gemini 支持，anthropic 忽略。
	JSONSchema          *JSONSchemaSpec // JSONSchema 非 nil 时优先于 JSONMode。
	ParallelToolCalls   *bool           // ParallelToolCalls 是 openai 的并行工具调用开关（仅 openai 显式传参，anthropic/gemini 原生支持多工具调用块，无需参数）。
	StreamIncludeUsage  *bool           // StreamIncludeUsage 控制 openai 的 stream_options.include_usage；nil 时默认 true（保持既有行为）。anthropic/gemini 的流式恒携带 usage。
	ExtraParams         map[string]any  // ExtraParams 厂商特有参数（如 thinking、do_sample、clear_thinking）。openai/anthropic 合并到请求体顶层；gemini 合并到 generationConfig。
	Model               string          // 为空则用 Provider 默认模型
	ToolsChoice         string          // ToolsChoice 工具调用策略：""/"auto" 由模型决定；"any"/"required" 强制调用一次。
	ReasoningEffort     string          // ReasoningEffort 思考力度（openai 的 reasoning_effort，仅 openai）。
	Messages            []Message       // 消息
	Stop                []string        // Stop 停止词。openai=stop / anthropic=stop_sequences / gemini=stopSequences。
	Tools               []Tool          // 工具调用
	MaxTokens           int             // <=0 时省略（Anthropic 协议要求必填，缺省补 1024）
	MaxCompletionTokens int             // MaxCompletionTokens 是 openai 的 max_completion_tokens 兼容字段（仅 openai）。
	RequestTimeout      time.Duration   // 本次请求的超时覆盖；>0 时覆盖客户端默认超时（如长输出探测需要更长的观测窗口）。
	JSONMode            bool            // 开启 JSON 输出
}

// Result 是一次调用的统一结果，流式与非流式共用。
// FinishReason 统一映射为 OpenAI 风格：stop / length / tool_calls。
type Result struct {
	Content string
	// ReasoningContent 思考内容（openai 方言的 reasoning_content /
	// anthropic thinking 块 / gemini thought part），无思考输出时为空。
	ReasoningContent string
	FinishReason     string
	ToolCalls        []ToolCall
	PromptTokens     int64
	CompletionTokens int64
	Chunks           int     // 流式时的 SSE 事件数
	TTFTMS           float64 // 流式时为首个有内容 chunk（含思考内容）到达延迟；未捕获到任何内容时为 -1
	LatencyMS        float64
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
func rawPost(ctx context.Context, hc *http.Client, url string, headers map[string]string, payload map[string]any) (*RawResult, error) {
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
			return err
		}
	}
	return sc.Err()
}

func milliSince(t time.Time) float64 {
	return float64(time.Since(t).Milliseconds())
}
