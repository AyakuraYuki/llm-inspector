package provider

// Anthropic Messages API 的 Provider 实现（手写 net/http，含 SSE 流式）。

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

const anthropicVersion = "2023-06-01"

var (
	_ Provider  = (*anthropicClient)(nil)
	_ RawCaller = (*anthropicClient)(nil)
)

type anthropicClient struct {
	hc      *http.Client
	baseURL string
	apiKey  string
	model   string
}

// NewAnthropic 创建 Anthropic 协议客户端。base_url 缺省补 /v1。
func NewAnthropic(baseURL, apiKey, model string, timeout time.Duration) Provider {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &anthropicClient{
		baseURL: base,
		apiKey:  apiKey,
		model:   model,
		hc:      newHTTPClient(timeout),
	}
}

func (c *anthropicClient) Protocol() string { return "anthropic" }
func (c *anthropicClient) Model() string    { return c.model }

func (c *anthropicClient) headers() map[string]string {
	return map[string]string{
		"x-api-key":         c.apiKey,
		"anthropic-version": anthropicVersion,
	}
}

// anthropicMessage 的 Content 为 string 或 []map[string]any（内容块数组，
// 用于 tool_use / tool_result 回传场景）。
type anthropicMessage struct {
	Content any    `json:"content"`
	Role    string `json:"role"`
}

type anthropicTool struct {
	InputSchema map[string]any `json:"input_schema"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
}

type anthropicToolChoice struct {
	Type string `json:"type"` // auto / any / none
}

type anthropicRequest struct {
	Temperature   *float64             `json:"temperature,omitempty"`
	TopP          *float64             `json:"top_p,omitempty"`
	ToolChoice    *anthropicToolChoice `json:"tool_choice,omitempty"`
	Model         string               `json:"model"`
	System        string               `json:"system,omitempty"`
	Messages      []anthropicMessage   `json:"messages"`
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Tools         []anthropicTool      `json:"tools,omitempty"`
	MaxTokens     int                  `json:"max_tokens"`
	Stream        bool                 `json:"stream"`
}

type anthropicContentBlock struct {
	Input    map[string]any `json:"input"`
	Type     string         `json:"type"`
	Text     string         `json:"text"`
	Thinking string         `json:"thinking"`
	ID       string         `json:"id"`
	Name     string         `json:"name"`
}

type anthropicUsage struct {
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

type anthropicResponse struct {
	StopReason string                  `json:"stop_reason"`
	Content    []anthropicContentBlock `json:"content"`
	Usage      anthropicUsage          `json:"usage"`
}

func (c *anthropicClient) buildRequest(req *params.Request, stream bool) (map[string]any, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	ar := anthropicRequest{
		Model:         model,
		MaxTokens:     req.MaxTokens,
		Temperature:   req.Temperature,
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        stream,
	}
	if ar.MaxTokens <= 0 {
		ar.MaxTokens = 1024 // Anthropic 要求 max_tokens 必填
	}
	var systemParts []string
	for _, m := range req.Messages {
		switch strings.ToLower(m.Role) {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var blocks []map[string]any
				if m.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
				}
				for _, tc := range m.ToolCalls {
					input := map[string]any{}
					_ = json.Unmarshal([]byte(tc.Arguments), &input)
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
				}
				ar.Messages = append(ar.Messages, anthropicMessage{Role: "assistant", Content: blocks})
			} else {
				ar.Messages = append(ar.Messages, anthropicMessage{Role: "assistant", Content: m.Content})
			}
		case "tool":
			ar.Messages = append(ar.Messages, anthropicMessage{
				Role: "user",
				Content: []map[string]any{
					{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content},
				},
			})
		default:
			ar.Messages = append(ar.Messages, anthropicMessage{Role: "user", Content: m.Content})
		}
	}
	ar.System = strings.Join(systemParts, "\n")
	for _, t := range req.Tools {
		ar.Tools = append(ar.Tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	if len(ar.Tools) > 0 && (req.ToolsChoice == "any" || req.ToolsChoice == "required") {
		ar.ToolChoice = &anthropicToolChoice{Type: "any"}
	}
	return mergeExtraParams(ar, req.ExtraParams)
}

// mergeExtraParams 把请求结构体转为 map 并合并厂商特有参数（如 thinking）。
// extras 中的键会覆盖结构体中的同名键。
func mergeExtraParams(v any, extras map[string]any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if err = json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	maps.Copy(body, extras)
	return body, nil
}

// anthropicStopReason 映射为 OpenAI 风格的 finish_reason。
func anthropicStopReason(s string) string {
	switch s {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return s
	}
}

// chatHTTPClient 返回本次请求使用的 http.Client；RequestTimeout>0 时用副本覆盖默认超时。
func (c *anthropicClient) chatHTTPClient(req *params.Request) *http.Client {
	hc := c.hc
	if req.RequestTimeout > 0 {
		clone := *c.hc
		clone.Timeout = req.RequestTimeout
		hc = &clone
	}
	return hc
}

func (c *anthropicClient) Chat(ctx context.Context, req *params.Request) (*params.Result, error) {
	start := time.Now()
	body, err := c.buildRequest(req, false)
	if err != nil {
		return nil, err
	}
	var resp anthropicResponse
	if err = doJSON(ctx, c.chatHTTPClient(req), http.MethodPost, c.baseURL+"/messages", c.headers(), body, &resp); err != nil {
		return nil, err
	}
	r := &params.Result{LatencyMS: milliSince(start)}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			r.Content += b.Text
		case "thinking":
			r.ReasoningContent += b.Thinking
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			r.ToolCalls = append(r.ToolCalls, params.ToolCall{ID: b.ID, Name: b.Name, Arguments: string(args)})
		}
	}
	r.FinishReason = anthropicStopReason(resp.StopReason)
	r.PromptTokens = resp.Usage.InputTokens
	r.CompletionTokens = resp.Usage.OutputTokens
	r.CachedInputTokens = resp.Usage.CacheReadInputTokens
	return r, nil
}

// anthropicStreamEvent 是 SSE data 载荷的通用结构。
type anthropicStreamEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
}

func (c *anthropicClient) Stream(ctx context.Context, req *params.Request) (*params.Result, error) {
	start := time.Now()
	body, err := c.buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	r := &params.Result{TTFTMS: -1}
	var sb, rb strings.Builder
	var toolID string
	var toolName, toolArgs strings.Builder

	err = ssePost(ctx, c.chatHTTPClient(req), c.baseURL+"/messages", c.headers(), body,
		func(data []byte) error {
			var ev anthropicStreamEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return nil // 忽略无法解析的事件
			}
			r.Chunks++
			switch ev.Type {
			case "message_start":
				r.PromptTokens = ev.Message.Usage.InputTokens
				r.CachedInputTokens = ev.Message.Usage.CacheReadInputTokens
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					toolID = ev.ContentBlock.ID
					toolName.Reset()
					toolName.WriteString(ev.ContentBlock.Name)
					toolArgs.Reset()
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					if r.TTFTMS < 0 && ev.Delta.Text != "" {
						r.TTFTMS = milliSince(start)
					}
					sb.WriteString(ev.Delta.Text)
				case "thinking_delta":
					if r.TTFTMS < 0 && ev.Delta.Thinking != "" {
						r.TTFTMS = milliSince(start)
					}
					rb.WriteString(ev.Delta.Thinking)
				case "input_json_delta":
					toolArgs.WriteString(ev.Delta.PartialJSON)
				}
			case "message_delta":
				r.FinishReason = anthropicStopReason(ev.Delta.StopReason)
				if ev.Usage.OutputTokens > 0 {
					r.CompletionTokens = ev.Usage.OutputTokens
				}
			case "error":
				return &HTTPError{StatusCode: 500, Body: string(data)}
			}
			return nil
		})
	if toolName.Len() > 0 {
		r.ToolCalls = append(r.ToolCalls, params.ToolCall{ID: toolID, Name: toolName.String(), Arguments: toolArgs.String()})
	}
	r.Content = sb.String()
	r.ReasoningContent = rb.String()
	r.LatencyMS = milliSince(start)
	if err != nil {
		// 超时/中断时也返回已累积的内容，便于上层统计"截止出错前的输出量"。
		return r, err
	}
	return r, nil
}

func (c *anthropicClient) Models(ctx context.Context) ([]string, error) {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doJSON(ctx, c.hc, http.MethodGet, c.baseURL+"/models", c.headers(), nil, &resp); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// RawChat 直接 POST /messages，绕过类型校验。
func (c *anthropicClient) RawChat(ctx context.Context, req *RawRequest) (*RawResult, error) {
	headers := map[string]string{"anthropic-version": anthropicVersion}
	switch {
	case req.OmitAuth:
	case req.OverrideAuth != "":
		headers["x-api-key"] = req.OverrideAuth
	default:
		headers["x-api-key"] = c.apiKey
	}
	return rawPost(ctx, c.hc, c.baseURL+"/messages", headers, req.Payload)
}
