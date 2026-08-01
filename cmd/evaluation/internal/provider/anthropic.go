// Anthropic Messages API 的 Provider 实现（手写 net/http，含 SSE 流式）。
package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const anthropicVersion = "2023-06-01"

type anthropicClient struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
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
		hc:      &http.Client{Timeout: timeout},
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

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicToolChoice struct {
	Type string `json:"type"` // auto / any / none
}

type anthropicRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"`
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Temperature *float64             `json:"temperature,omitempty"`
	Stream      bool                 `json:"stream,omitempty"`
	Tools       []anthropicTool      `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
}

type anthropicContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type anthropicUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

func (c *anthropicClient) buildRequest(req *Request, stream bool) anthropicRequest {
	model := req.Model
	if model == "" {
		model = c.model
	}
	ar := anthropicRequest{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
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
			ar.Messages = append(ar.Messages, anthropicMessage{Role: "assistant", Content: m.Content})
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
	return ar
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

func (c *anthropicClient) Chat(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	var resp anthropicResponse
	err := doJSON(ctx, c.hc, http.MethodPost, c.baseURL+"/messages", c.headers(),
		c.buildRequest(req, false), &resp)
	if err != nil {
		return nil, err
	}
	r := &Result{LatencyMS: msSince(start)}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			r.Content += b.Text
		case "tool_use":
			args, _ := json.Marshal(b.Input)
			r.ToolCalls = append(r.ToolCalls, ToolCall{Name: b.Name, Arguments: string(args)})
		}
	}
	r.FinishReason = anthropicStopReason(resp.StopReason)
	r.PromptTokens = resp.Usage.InputTokens
	r.CompletionTokens = resp.Usage.OutputTokens
	return r, nil
}

// anthropicStreamEvent 是 SSE data 载荷的通用结构。
type anthropicStreamEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage anthropicUsage `json:"usage"`
}

func (c *anthropicClient) Stream(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	r := &Result{TTFTMS: -1}
	var sb strings.Builder
	var toolName, toolArgs strings.Builder

	err := ssePost(ctx, c.hc, c.baseURL+"/messages", c.headers(), c.buildRequest(req, true),
		func(data []byte) error {
			var ev anthropicStreamEvent
			if err := json.Unmarshal(data, &ev); err != nil {
				return nil // 忽略无法解析的事件
			}
			r.Chunks++
			switch ev.Type {
			case "message_start":
				r.PromptTokens = ev.Message.Usage.InputTokens
			case "content_block_start":
				if ev.ContentBlock.Type == "tool_use" {
					toolName.Reset()
					toolName.WriteString(ev.ContentBlock.Name)
					toolArgs.Reset()
				}
			case "content_block_delta":
				switch ev.Delta.Type {
				case "text_delta":
					if r.TTFTMS < 0 && ev.Delta.Text != "" {
						r.TTFTMS = msSince(start)
					}
					sb.WriteString(ev.Delta.Text)
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
	if err != nil {
		return nil, err
	}
	if toolName.Len() > 0 {
		r.ToolCalls = append(r.ToolCalls, ToolCall{Name: toolName.String(), Arguments: toolArgs.String()})
	}
	r.Content = sb.String()
	r.LatencyMS = msSince(start)
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

var _ Provider = (*anthropicClient)(nil)
