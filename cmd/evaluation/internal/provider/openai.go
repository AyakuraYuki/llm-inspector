package provider

// OpenAI 兼容协议的 Provider 实现（基于官方 SDK，禁用重试以观察服务真实行为）。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/openai/openai-go/shared"
)

type openaiProvider struct {
	client  openai.Client
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
}

// NewOpenAI 创建 OpenAI 兼容端点客户端。
func NewOpenAI(baseURL, apiKey, model string, timeout time.Duration) Provider {
	c := openai.NewClient(
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
		option.WithRequestTimeout(timeout),
	)
	return &openaiProvider{
		client:  c,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		hc:      &http.Client{Timeout: timeout},
	}
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
			if len(m.ToolCalls) > 0 {
				asst := openai.ChatCompletionAssistantMessageParam{}
				if m.Content != "" {
					asst.Content.OfString = openai.String(m.Content)
				}
				for _, tc := range m.ToolCalls {
					asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
				params.Messages = append(params.Messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			} else {
				params.Messages = append(params.Messages, openai.AssistantMessage(m.Content))
			}
		case "tool":
			params.Messages = append(params.Messages, openai.ToolMessage(m.Content, m.ToolCallID))
		default:
			params.Messages = append(params.Messages, openai.UserMessage(m.Content))
		}
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.MaxCompletionTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxCompletionTokens))
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.TopP != nil {
		params.TopP = openai.Float(*req.TopP)
	}
	if req.FrequencyPenalty != nil {
		params.FrequencyPenalty = openai.Float(*req.FrequencyPenalty)
	}
	if req.PresencePenalty != nil {
		params.PresencePenalty = openai.Float(*req.PresencePenalty)
	}
	if req.Seed != nil {
		params.Seed = openai.Int(*req.Seed)
	}
	if len(req.Stop) == 1 {
		params.Stop.OfString = openai.String(req.Stop[0])
	} else if len(req.Stop) > 1 {
		params.Stop.OfStringArray = req.Stop
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	if req.JSONSchema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   req.JSONSchema.Name,
					Schema: req.JSONSchema.Schema,
					Strict: openai.Bool(req.JSONSchema.Strict),
				},
			},
		}
	} else if req.JSONMode {
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
	if len(params.Tools) > 0 {
		if strings.EqualFold(req.ToolsChoice, "any") {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoAuto)),
			}
		} else if strings.EqualFold(req.ToolsChoice, "required") {
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoRequired)),
			}
		}
	}
	if req.ParallelToolCalls != nil {
		params.ParallelToolCalls = openai.Bool(*req.ParallelToolCalls)
	}
	if stream {
		includeUsage := true
		if req.StreamIncludeUsage != nil {
			includeUsage = *req.StreamIncludeUsage
		}
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(includeUsage),
		}
	}
	if len(req.ExtraParams) > 0 {
		params.SetExtraFields(req.ExtraParams)
	}
	return params
}

// extraString 从 SDK 响应的 ExtraFields 中取字符串字段（如方言的 reasoning_content）。
func extraString(fields map[string]respjson.Field, key string) string {
	f, ok := fields[key]
	if !ok || !f.Valid() {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(f.Raw()), &s); err != nil {
		return ""
	}
	return s
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
		r.ReasoningContent = extraString(c.Message.JSON.ExtraFields, "reasoning_content")
		r.FinishReason = string(c.FinishReason)
		for _, tc := range c.Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				ID:        tc.ID,
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
	var sb, rb strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		r.Chunks++
		if r.TTFTMS < 0 && len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			r.TTFTMS = msSince(start)
		}
		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			sb.WriteString(c.Delta.Content)
			rb.WriteString(extraString(c.Delta.JSON.ExtraFields, "reasoning_content"))
			if c.FinishReason != "" {
				r.FinishReason = string(c.FinishReason)
			}
			for _, tc := range c.Delta.ToolCalls {
				r.ToolCalls = append(r.ToolCalls, ToolCall{
					ID:        tc.ID,
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
	r.ReasoningContent = rb.String()
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

// RawChat 直接 POST /chat/completions，绕过 SDK 类型校验。
func (p *openaiProvider) RawChat(ctx context.Context, req *RawRequest) (*RawResult, error) {
	headers := map[string]string{}
	switch {
	case req.OmitAuth:
	case req.OverrideAuth != "":
		headers["Authorization"] = "Bearer " + req.OverrideAuth
	default:
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	return rawPost(ctx, p.hc, p.baseURL+"/chat/completions", headers, req.Payload)
}

var _ RawCaller = (*openaiProvider)(nil)

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
