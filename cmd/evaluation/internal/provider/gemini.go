// Gemini generateContent API 的 Provider 实现（手写 net/http，含 SSE 流式）。
package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type geminiClient struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
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
		hc:      &http.Client{Timeout: timeout},
	}
}

func (c *geminiClient) Protocol() string { return "gemini" }
func (c *geminiClient) Model() string    { return c.model }

func (c *geminiClient) headers() map[string]string {
	return map[string]string{"x-goog-api-key": c.apiKey}
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode string `json:"mode"` // AUTO / ANY / NONE
	} `json:"functionCallingConfig"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (c *geminiClient) buildRequest(req *Request) geminiRequest {
	gr := geminiRequest{}
	var systemParts []string
	for _, m := range req.Messages {
		switch strings.ToLower(m.Role) {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "assistant":
			gr.Contents = append(gr.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: m.Content}},
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
	}
	if req.JSONMode {
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
	return gr
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

// applyResponse 把 Gemini 响应（完整或流式 chunk）合并进 Result。
func applyGeminiResponse(r *Result, resp *geminiResponse, onText func(string)) {
	if len(resp.Candidates) > 0 {
		cand := resp.Candidates[0]
		// 取最后一个 text part，避开可能的 thinking part
		for i := len(cand.Content.Parts) - 1; i >= 0; i-- {
			part := cand.Content.Parts[i]
			if part.Text != "" {
				onText(part.Text)
				break
			}
		}
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				r.ToolCalls = append(r.ToolCalls, ToolCall{
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
}

func (c *geminiClient) modelPath(req *Request) string {
	model := req.Model
	if model == "" {
		model = c.model
	}
	return c.baseURL + "/models/" + model
}

func (c *geminiClient) Chat(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	var resp geminiResponse
	err := doJSON(ctx, c.hc, http.MethodPost, c.modelPath(req)+":generateContent", c.headers(),
		c.buildRequest(req), &resp)
	if err != nil {
		return nil, err
	}
	r := &Result{LatencyMS: msSince(start)}
	applyGeminiResponse(r, &resp, func(t string) { r.Content += t })
	return r, nil
}

func (c *geminiClient) Stream(ctx context.Context, req *Request) (*Result, error) {
	start := time.Now()
	r := &Result{TTFTMS: -1}
	var sb strings.Builder
	err := ssePost(ctx, c.hc, c.modelPath(req)+":streamGenerateContent?alt=sse", c.headers(),
		c.buildRequest(req),
		func(data []byte) error {
			var chunk geminiResponse
			if err := json.Unmarshal(data, &chunk); err != nil {
				return nil // 忽略无法解析的 chunk
			}
			r.Chunks++
			applyGeminiResponse(r, &chunk, func(t string) {
				if r.TTFTMS < 0 && t != "" {
					r.TTFTMS = msSince(start)
				}
				sb.WriteString(t)
			})
			return nil
		})
	if err != nil {
		return nil, err
	}
	r.Content = sb.String()
	r.LatencyMS = msSince(start)
	return r, nil
}

func (c *geminiClient) Models(ctx context.Context) ([]string, error) {
	var resp struct {
		Models []struct {
			Name string `json:"name"` // 形如 "models/gemini-2.0-flash"
		} `json:"models"`
	}
	if err := doJSON(ctx, c.hc, http.MethodGet, c.baseURL+"/models", c.headers(), nil, &resp); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
	}
	return ids, nil
}

var _ Provider = (*geminiClient)(nil)
