package provider

// Gemini generateContent API 的 Provider 实现（手写 net/http，含 SSE 流式）。

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

var (
	_ Provider  = (*geminiClient)(nil)
	_ RawCaller = (*geminiClient)(nil)
)

type geminiClient struct {
	hc      *http.Client
	baseURL string
	apiKey  string
	model   string
}

// NewGemini 创建 Gemini 协议客户端。base_url 缺省补 /v1beta。
func NewGemini(baseURL, apiKey, model string, timeout time.Duration) Provider {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1beta") {
		base += "/v1beta"
	}
	return &geminiClient{
		baseURL: base,
		apiKey:  apiKey,
		model:   model,
		hc:      newHTTPClient(timeout),
	}
}

func (c *geminiClient) Protocol() string { return "gemini" }
func (c *geminiClient) Model() string    { return c.model }

func (c *geminiClient) headers() map[string]string {
	return map[string]string{"x-goog-api-key": c.apiKey}
}

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	ToolUsePromptTokenCount int64 `json:"toolUsePromptTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Thought          bool   `json:"thought,omitempty"`
	ThoughtSignature string `json:"thoughtSignature,omitempty"`

	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Response     map[string]any `json:"response"`
	WillContinue bool           `json:"willContinue,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"topP,omitempty"`
	Seed             *int64         `json:"seed,omitempty"`
	ResponseSchema   map[string]any `json:"responseSchema,omitempty"`
	ResponseMIMEType string         `json:"responseMimeType,omitempty"`
	StopSequences    []string       `json:"stopSequences,omitempty"`
	MaxOutputTokens  int            `json:"maxOutputTokens,omitempty"`
}

type geminiFunctionDeclaration struct {
	Parameters  map[string]any `json:"parameters,omitempty"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode string `json:"mode"` // AUTO / ANY / NONE
	} `json:"functionCallingConfig"`
}

type geminiModel struct {
	Name                       string   `json:"name"`
	BaseModelId                string   `json:"baseModelId"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	InputTokenLimit            int64    `json:"inputTokenLimit"`
	OutputTokenLimit           int64    `json:"outputTokenLimit"`
	Temperature                float64  `json:"temperature"`
	MaxTemperature             float64  `json:"maxTemperature"`
	TopP                       float64  `json:"topP"`
	TopK                       int64    `json:"topK"`
	Thinking                   bool     `json:"thinking"`
}

type geminiModelsResponse struct {
	NextPageToken string        `json:"nextPageToken,omitempty"`
	Models        []geminiModel `json:"models"`
}

func (c *geminiClient) buildRequest(req *params.Request) (map[string]any, error) {
	gr := geminiRequest{}
	var systemParts []string
	for _, m := range req.Messages {
		switch strings.ToLower(m.Role) {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "assistant":
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
				parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: args}})
			}
			if len(parts) == 0 {
				parts = append(parts, geminiPart{Text: ""})
			}
			gr.Contents = append(gr.Contents, geminiContent{Role: "model", Parts: parts})
		case "tool":
			// Gemini 用函数名关联结果；结果需为对象，字符串结果包一层
			resp := map[string]any{}
			if err := json.Unmarshal([]byte(m.Content), &resp); err != nil {
				resp = map[string]any{"result": m.Content}
			}
			gr.Contents = append(gr.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{FunctionResponse: &geminiFunctionResponse{Name: m.Name, Response: resp}}},
			})
		default:
			gr.Contents = append(gr.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		}
	}
	if len(systemParts) > 0 {
		gr.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: strings.Join(systemParts, "\n")}},
		}
	}
	gc := &geminiGenerationConfig{
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
		StopSequences:   req.Stop,
		Seed:            req.Seed,
	}
	if req.JSONSchema != nil {
		gc.ResponseMIMEType = "application/json"
		gc.ResponseSchema = req.JSONSchema.Schema
	} else if req.JSONMode {
		gc.ResponseMIMEType = "application/json"
	}
	gr.GenerationConfig = gc
	if len(req.Tools) > 0 {
		var decls []geminiFunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, geminiFunctionDeclaration(t))
		}
		gr.Tools = []geminiTool{{FunctionDeclarations: decls}}
		if req.ToolsChoice == "any" || req.ToolsChoice == "required" {
			tc := &geminiToolConfig{}
			tc.FunctionCallingConfig.Mode = "ANY"
			gr.ToolConfig = tc
		}
	}
	body, err := mergeExtraParams(gr, nil)
	if err != nil {
		return nil, err
	}
	// Gemini 的厂商特有参数（如 thinkingConfig）位于 generationConfig 内
	if len(req.ExtraParams) > 0 {
		gcMap, _ := body["generationConfig"].(map[string]any)
		if gcMap == nil {
			gcMap = map[string]any{}
		}
		maps.Copy(gcMap, req.ExtraParams)
		body["generationConfig"] = gcMap
	}
	return body, nil
}

// geminiFinishReason 映射为 OpenAI 风格的 finish_reason。
func geminiFinishReason(s string) string {
	switch s {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default:
		return strings.ToLower(s)
	}
}

// applyGeminiResponse 把 Gemini 响应（完整或流式 chunk）合并进 params.Result。
// onText 接收正文文本增量；onFirst 在首个有内容的文本（含思考）到达时回调，
// 供流式调用记录 TTFT。非流式调用（Chat）传 no-op 即可。
func applyGeminiResponse(r *params.Result, resp *geminiResponse, onText, onFirst func(string)) {
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		for _, part := range cand.Content.Parts {
			if part.Thought && part.Text != "" {
				onFirst(part.Text)
				r.ReasoningContent += part.Text
			}
		}
		// 取最后一个非 thought 的 text part
		for _, part := range slices.Backward(cand.Content.Parts) {

			if part.Text != "" && !part.Thought {
				onFirst(part.Text)
				onText(part.Text)
				break
			}
		}
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				r.ToolCalls = append(r.ToolCalls, params.ToolCall{
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				})
			}
		}
		if cand.FinishReason != "" {
			r.FinishReason = geminiFinishReason(cand.FinishReason)
		}
	}
	if resp.UsageMetadata.PromptTokenCount > 0 {
		r.PromptTokens = resp.UsageMetadata.PromptTokenCount
	}
	if resp.UsageMetadata.CandidatesTokenCount > 0 {
		r.CompletionTokens = resp.UsageMetadata.CandidatesTokenCount
	}
	if resp.UsageMetadata.CachedContentTokenCount > 0 {
		r.CachedInputTokens = resp.UsageMetadata.CachedContentTokenCount
	}
}

func (c *geminiClient) modelPath(req *params.Request) string {
	model := req.Model
	if model == "" {
		model = c.model
	}
	return c.baseURL + "/models/" + model
}

// chatHTTPClient 返回本次请求使用的 http.Client；RequestTimeout>0 时用副本覆盖默认超时。
func (c *geminiClient) chatHTTPClient(req *params.Request) *http.Client {
	hc := c.hc
	if req.RequestTimeout > 0 {
		clone := *c.hc
		clone.Timeout = req.RequestTimeout
		hc = &clone
	}
	return hc
}

func (c *geminiClient) Chat(ctx context.Context, req *params.Request) (*params.Result, error) {
	start := time.Now()
	body, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}
	var resp geminiResponse
	if err := doJSON(ctx, c.chatHTTPClient(req), http.MethodPost, c.modelPath(req)+":generateContent", c.headers(), body, &resp); err != nil {
		return nil, err
	}
	r := &params.Result{LatencyMS: milliSince(start)}
	noop := func(string) {}
	applyGeminiResponse(r, &resp, func(t string) { r.Content += t }, noop)
	return r, nil
}

func (c *geminiClient) Stream(ctx context.Context, req *params.Request) (*params.Result, error) {
	start := time.Now()
	body, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}
	r := &params.Result{TTFTMS: -1}
	var sb strings.Builder
	err = ssePost(ctx, c.chatHTTPClient(req), c.modelPath(req)+":streamGenerateContent?alt=sse", c.headers(), body,
		func(data []byte) error {
			var chunk geminiResponse
			if err := json.Unmarshal(data, &chunk); err != nil {
				return nil // 忽略无法解析的 chunk
			}
			r.Chunks++
			onFirst := func(t string) {
				if r.TTFTMS < 0 && t != "" {
					r.TTFTMS = milliSince(start)
				}
			}
			applyGeminiResponse(r, &chunk, func(t string) {
				onFirst(t)
				sb.WriteString(t)
			}, onFirst)
			return nil
		})
	r.Content = sb.String()
	r.LatencyMS = milliSince(start)
	if err != nil {
		// 超时/中断时也返回已累积的内容，便于上层统计"截止出错前的输出量"。
		return r, err
	}
	return r, nil
}

func (c *geminiClient) Models(ctx context.Context) ([]string, error) {
	var resp geminiModelsResponse
	if err := doJSON(ctx, c.hc, http.MethodGet, c.baseURL+"/models", c.headers(), nil, &resp); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
	}
	return ids, nil
}

// RawChat 直接 POST :generateContent，绕过类型校验。
func (c *geminiClient) RawChat(ctx context.Context, req *RawRequest) (*RawResult, error) {
	headers := map[string]string{}
	switch {
	case req.OmitAuth:
	case req.OverrideAuth != "":
		headers["x-goog-api-key"] = req.OverrideAuth
	default:
		headers["x-goog-api-key"] = c.apiKey
	}
	return rawPost(ctx, c.hc, c.baseURL+"/models/"+c.model+":generateContent", headers, req.Payload)
}
