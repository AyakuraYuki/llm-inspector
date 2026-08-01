// OpenAI 兼容协议的 Provider 实现（基于官方 SDK，禁用重试以观察服务真实行为）。
package provider

import (
	"context"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type openaiProvider struct {
	client openai.Client
	model  string
}

// NewOpenAI 创建 OpenAI 兼容端点客户端。
func NewOpenAI(baseURL, apiKey, model string, timeout time.Duration) Provider {
	c := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(timeout),
	)
	return &openaiProvider{client: c, model: model}
}

func (p *openaiProvider) Protocol() string { return "openai" }
func (p *openaiProvider) Model() string    { return p.model }

func (p *openaiProvider) buildParams(req *Request, stream bool) openai.ChatCompletionNewParams {
	model := req.Model
	if model == "" {
		model = p.model
	}
	params := openai.ChatCompletionNewParams{
		Model: openai.ChatModel(model),
	}
	for _, m := range req.Messages {
		switch strings.ToLower(m.Role) {
		case "system":
			params.Messages = append(params.Messages, openai.SystemMessage(m.Content))
		case "assistant":
			params.Messages = append(params.Messages, openai.AssistantMessage(m.Content))
		default:
			params.Messages = append(params.Messages, openai.UserMessage(m.Content))
		}
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.JSONMode {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(t.Parameters),
			},
		})
	}
	if len(params.Tools) > 0 && (req.ToolsChoice == "any" || req.ToolsChoice == "required") {
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoRequired)),
		}
	}
	if stream {
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}
	return params
}

// Chat 发起非流式调用。
func (p *openaiProvider) Chat(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	resp, err := p.client.Chat.Completions.New(ctx, p.buildParams(req, false))
	if err != nil {
		return nil, err
	}
	r := &Result{LatencyMS: msSince(start)}
	if len(resp.Choices) > 0 {
		c := resp.Choices[0]
		r.Content = c.Message.Content
		r.FinishReason = string(c.FinishReason)
		for _, tc := range c.Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}
	r.PromptTokens = resp.Usage.PromptTokens
	r.CompletionTokens = resp.Usage.CompletionTokens
	return r, nil
}

// Stream 发起流式调用，记录 TTFT 并聚合全部增量内容。
func (p *openaiProvider) Stream(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	stream := p.client.Chat.Completions.NewStreaming(ctx, p.buildParams(req, true))
	defer stream.Close()

	r := &Result{TTFTMS: -1}
	var sb strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		r.Chunks++
		if r.TTFTMS < 0 && len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			r.TTFTMS = msSince(start)
		}
		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			sb.WriteString(c.Delta.Content)
			if c.FinishReason != "" {
				r.FinishReason = string(c.FinishReason)
			}
			for _, tc := range c.Delta.ToolCalls {
				r.ToolCalls = append(r.ToolCalls, ToolCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				})
			}
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			r.PromptTokens = chunk.Usage.PromptTokens
			r.CompletionTokens = chunk.Usage.CompletionTokens
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	r.Content = sb.String()
	r.LatencyMS = msSince(start)
	return r, nil
}

// Models 返回 GET /models 的模型 id 列表。
func (p *openaiProvider) Models(ctx context.Context) ([]string, error) {
	page, err := p.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Data))
	for _, m := range page.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
