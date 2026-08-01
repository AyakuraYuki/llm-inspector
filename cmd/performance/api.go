package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
)

// newTransport 构建连接池调优后的 Transport。
//
// http.DefaultTransport 的 MaxIdleConnsPerHost 默认只有 2，在近万并发下，
// 请求结束后连接几乎立刻被关闭而不是保留复用，下一轮请求又要重新三次握手
// （HTTPS 还要再加一次 TLS 握手）。这部分开销会叠加进 TTFT/延迟指标，
// 让压测结果失真（看起来像上游变慢，实际是本地连接抖动）。
// 这里把 MaxIdleConnsPerHost/MaxIdleConns 按本次压测的最大并发数调大，
// 让并发协程之间尽量复用连接而不是互相"抢"仅有的 2 个空闲连接。
func newTransport(maxConcurrency int) *http.Transport {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	return &http.Transport{
		MaxIdleConns:        maxConcurrency * 2,
		MaxIdleConnsPerHost: maxConcurrency,
		MaxConnsPerHost:     0, // 不设上限，实际并发量由 -concurrency 档位控制
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	}
}

// sharedClient 复用连接池；超时由 context 控制。
// 初始按预热并发（warmupConcurrency）建池，正式压测前会由
// configureSharedClient 按本次实际最大并发重建。
var sharedClient = &http.Client{Transport: newTransport(warmupConcurrency)}

// configureSharedClient 按本次压测将要用到的最大并发数重建连接池。
// 应在解析完 -concurrency 参数、preflightCheck/正式压测开始之前调用一次。
func configureSharedClient(maxConcurrency int) {
	sharedClient.Transport = newTransport(maxConcurrency)
}

// classifyNetError 将 sharedClient.Do 返回的传输层错误细分为更具体的类型，
// 便于区分"我们自己的超时预算到了"“上游拒绝/重置连接”"DNS 挂了"等不同故障域。
// 判断顺序很重要：更具体的原因要排在通用的 net.Error.Timeout() 之前，
// 否则例如 DNS 超时会被笼统地归为 net_timeout 而丢失"是 DNS 的问题"这一信息。
func classifyNetError(err error) ErrorType {
	if errors.Is(err, context.Canceled) {
		return ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTypeTimeout
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return ErrorTypeDNS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ErrorTypeConnRefused
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return ErrorTypeConnReset
	}
	if isTLSError(err) {
		return ErrorTypeTLS
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return ErrorTypeNetTimeout
	}
	return ErrorTypeConnect
}

// isTLSError 判断错误是否源自 TLS 握手/证书校验失败。
func isTLSError(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	return false
}

// classifyHTTPStatus 按状态码把非 200 响应细分为限流/服务端错误/其他客户端错误。
func classifyHTTPStatus(code int) ErrorType {
	switch {
	case code == http.StatusTooManyRequests:
		return ErrorTypeRateLimit
	case code >= 500:
		return ErrorTypeServerError
	default:
		return ErrorTypeHTTP
	}
}

const (
	streamTimeout = 30 * time.Minute
	imageTimeout  = 30 * time.Minute
)

type doSSERequest func(context.Context, BenchmarkConfig, ModelSpec) RequestMetrics

// doSSERequests 声明了受支持的接口协议类型，并且可根据接口协议类型获取相应的
// 流式调用方法。
var doSSERequests = map[Provider]doSSERequest{
	ProviderAnthropic:      doAnthropicRequest,
	ProviderGemini:         doGeminiRequest,
	ProviderOpenAI:         doOpenAIRequest,
	ProviderOpenAIImage:    doImageRequest,
	ProviderOpenAIResponse: doOpenAIResponseRequest,
	ProviderBaseline:       doBaselineRequest,
}

// firstByteTracker 包装 io.Reader，精确记录首字节到达时刻。
type firstByteTracker struct {
	r         io.Reader
	t0        time.Time
	firstByte *float64
	once      sync.Once
}

func (t *firstByteTracker) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if n > 0 {
		t.once.Do(func() {
			*t.firstByte = time.Since(t.t0).Seconds() * 1000
		})
	}
	return
}

// doAnthropicRequest 向 /v1/messages 发起 SSE 流式请求。
func doAnthropicRequest(ctx context.Context, cfg BenchmarkConfig, model ModelSpec) RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":      model.Name,
		"max_tokens": 512,
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": cfg.BuildPrompt()}},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.PickToken())
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	return parseStreamMetrics(t0, resp.Body)
}

// doOpenAIRequest 向 /v1/chat/completions 发起 SSE 流式请求。
func doOpenAIRequest(ctx context.Context, cfg BenchmarkConfig, model ModelSpec) RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":          model.Name,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"messages":       []map[string]any{{"role": "user", "content": cfg.BuildPrompt()}},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	return parseStreamMetrics(t0, resp.Body)
}

// doGeminiRequest 向 /v1beta/models/{model}:streamGenerateContent 发起 SSE 流式请求。
func doGeminiRequest(ctx context.Context, cfg BenchmarkConfig, model ModelSpec) RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]any{{"text": cfg.BuildPrompt()}},
			},
		},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", cfg.BaseURL, model.Name)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	return parseStreamMetrics(t0, resp.Body)
}

// doImageRequest 向 /v1/images/generations 发起同步请求。
func doImageRequest(ctx context.Context, cfg BenchmarkConfig, model ModelSpec) RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":  model.Name,
		"prompt": cfg.ImagePrompt,
		"n":      1,
		"size":   "1024x1024",
	})

	reqCtx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.BaseURL+"/v1/images/generations", bytes.NewReader(payload))
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.PickToken())

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	// 读完响应体确保连接可复用
	_, _ = io.Copy(io.Discard, resp.Body)
	return RequestMetrics{
		TotalLatency: time.Since(t0),
		Success:      true,
	}
}

// doOpenAIResponseRequest 向 /v1/responses 发起 SSE 流式请求（OpenAI Responses API）。
func doOpenAIResponseRequest(ctx context.Context, cfg BenchmarkConfig, model ModelSpec) RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":  model.Name,
		"input":  cfg.BuildPrompt(),
		"stream": true,
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.BaseURL+"/v1/responses", bytes.NewReader(payload))
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	return parseStreamMetrics(t0, resp.Body)
}

// doBaselineRequest 对完全不经过任何大模型调用的静态接口发起同步 GET 请求，
// 用来单独衡量网关/接入层本身（TLS 握手、鉴权、路由等）在各并发档位下的开销，
// 和模型推理耗时解耦。
// 请求头参照真实浏览器抓包，避免被网关按 UA/Referer/Sec-Fetch-* 拦截。
func doBaselineRequest(ctx context.Context, cfg BenchmarkConfig, _ ModelSpec) RequestMetrics {
	t0 := time.Now()

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cfg.BaseURL+"/api/user-agreement", nil)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: ErrorTypeConnect}
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)}
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Since(t0),
			Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
			ErrorType:    classifyHTTPStatus(resp.StatusCode),
		}
	}

	// 读完响应体确保连接可复用
	_, _ = io.Copy(io.Discard, resp.Body)
	return RequestMetrics{
		TotalLatency: time.Since(t0),
		Success:      true,
	}
}

// parseStreamMetrics 消费 SSE 流，提取 TTFT / tokens / e2e。
func parseStreamMetrics(t0 time.Time, body io.Reader) RequestMetrics {
	var firstByteMs float64
	tracker := &firstByteTracker{r: body, t0: t0, firstByte: &firstByteMs}

	var (
		ttftMs    float64
		ttftSet   bool
		promptT   int
		compT     int
		cachedT   int
		usageSet  bool
		textParts []string
	)

	scanner := bufio.NewScanner(tracker)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()
		obj := parseSSELine(line)
		if obj == nil {
			continue
		}
		if !ttftSet && sseHasOutputContent(obj) {
			ttftMs = time.Since(t0).Seconds() * 1000
			ttftSet = true
		}
		p, c, ct, _ := consumeSSEUsage(obj, &textParts)
		if c >= 0 {
			compT = c
			usageSet = true
			if p > 0 {
				promptT = p
			}
		} else if p > 0 {
			promptT = p
		}
		if ct >= 0 {
			cachedT = ct
		}
	}

	e2eMs := time.Since(t0).Seconds() * 1000

	// 流读取中途出错（连接被重置/网络中断等），不能当作正常结束处理，
	// 否则已收到的部分内容会让请求被误判为成功（只是输出被截断）。
	if scanErr := scanner.Err(); scanErr != nil {
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
			Error:        scanErr.Error(),
			ErrorType:    ErrorTypeStreamBroken,
		}
	}

	// 回退：用首字节时间估算 TTFT
	if !ttftSet {
		if firstByteMs > 0 {
			ttftMs = firstByteMs
		} else {
			ttftMs = e2eMs
		}
	}
	// 无 usage 时用字符数粗估
	if !usageSet && len(textParts) > 0 {
		joined := strings.Join(textParts, "")
		if strings.TrimSpace(joined) != "" {
			compT = max(1, len(joined)/4)
		}
	}

	if ttftMs <= 0 {
		return RequestMetrics{
			Success:      false,
			TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
			Error:        "no output content received",
			ErrorType:    ErrorTypeNoContent,
		}
	}

	return RequestMetrics{
		TTFT:              time.Duration(ttftMs * float64(time.Millisecond)),
		TotalLatency:      time.Duration(e2eMs * float64(time.Millisecond)),
		InputTokens:       promptT,
		OutputTokens:      compT,
		CachedInputTokens: max(0, cachedT),
		Success:           true,
	}
}

// parseSSELine 将 SSE 数据行解析为 JSON 对象，遇到空行/注释/[DONE] 返回 nil。
func parseSSELine(line string) map[string]any {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return nil
	}
	payload := line
	if strings.HasPrefix(line, "data:") {
		payload = strings.TrimSpace(line[5:])
	}
	if payload == "[DONE]" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return nil
	}
	return obj
}

// sseHasOutputContent 检测 SSE 对象是否包含可视文本输出或思考内容（用于标记 TTFT 时刻）。
//
// 推理类模型（Claude extended thinking、DeepSeek-R1 系 reasoning_content、
// Gemini thinking 等）会先流式输出思考内容，再输出正式回答。思考内容同样是
// 用户/调用方能感知到的首个 token，因此也应计入 TTFT，而不能等到正式回答
// 开始才打点——否则开启了思考的请求会被算出偏高、失真的 TTFT。
func sseHasOutputContent(obj map[string]any) bool {
	// Anthropic: content_block_delta，delta.text 为正式回答，
	// delta.type == "thinking_delta" 时 delta.thinking 为思考内容
	if t, _ := obj["type"].(string); t == "content_block_delta" {
		if delta, ok := obj["delta"].(map[string]any); ok {
			if text, _ := delta["text"].(string); text != "" {
				return true
			}
			if thinking, _ := delta["thinking"].(string); thinking != "" {
				return true
			}
		}
	}
	// OpenAI 兼容协议: choices[].delta.content 为正式回答，
	// delta.reasoning_content（DeepSeek-R1/多数网关）或 delta.reasoning（部分网关）为思考内容
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if delta, ok := c["delta"].(map[string]any); ok {
				if content, _ := delta["content"].(string); content != "" {
					return true
				}
				if reasoning, _ := delta["reasoning_content"].(string); reasoning != "" {
					return true
				}
				if reasoning, _ := delta["reasoning"].(string); reasoning != "" {
					return true
				}
			}
		}
	}
	// Gemini: candidates[].content.parts[].text
	// 思考内容同样落在 parts[].text 里（配合 thought:true 标记），此处按文本非空
	// 统一判断，天然覆盖思考内容，无需额外分支
	if candidates, ok := obj["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
					if part, ok := parts[0].(map[string]any); ok {
						if text, _ := part["text"].(string); text != "" {
							return true
						}
					}
				}
			}
		}
	}
	// OpenAI Responses API: response.output_text.delta 为正式回答，
	// response.reasoning_summary_text.delta / response.reasoning_text.delta 为思考内容
	if t, _ := obj["type"].(string); t == "response.output_text.delta" ||
		t == "response.reasoning_summary_text.delta" ||
		t == "response.reasoning_text.delta" {
		if delta, _ := obj["delta"].(string); delta != "" {
			return true
		}
	}
	return false
}

// consumeSSEUsage 从 SSE 对象中提取 usage（completion_tokens/output_tokens/cached_tokens）和文本片段。
// compT == -1 表示本事件无 usage 信息；cachedT == -1 表示本事件未携带缓存命中信息。
func consumeSSEUsage(obj map[string]any, textParts *[]string) (promptT, compT, cachedT int, source string) {
	compT = -1
	cachedT = -1

	// OpenAI 顶层 usage（stream_options include_usage 的最终 chunk）
	if usage, ok := obj["usage"].(map[string]any); ok {
		c := intFromMap(usage, "completion_tokens", "output_tokens")
		if c >= 0 {
			compT = c
			promptT = max(0, intFromMap(usage, "prompt_tokens", "input_tokens"))
			source = "usage"
		}
		// OpenAI: usage.prompt_tokens_details.cached_tokens
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			cachedT = intFromMap(details, "cached_tokens")
		}
	}

	// Anthropic message_delta 事件里的 usage
	if compT < 0 {
		if t, _ := obj["type"].(string); t == "message_delta" {
			if usage, ok := obj["usage"].(map[string]any); ok {
				c := intFromMap(usage, "output_tokens")
				if c >= 0 {
					compT = c
					source = "usage"
				}
			}
		}
	}

	// Anthropic message_start 事件里的初始 usage（携带 input_tokens 和缓存读取 token 数，此时尚无 output_tokens）
	if t, _ := obj["type"].(string); t == "message_start" {
		if msg, ok := obj["message"].(map[string]any); ok {
			if usage, ok := msg["usage"].(map[string]any); ok {
				p := intFromMap(usage, "input_tokens")
				if p >= 0 {
					promptT = p
				}
				cachedT = intFromMap(usage, "cache_read_input_tokens")
			}
		}
	}

	// Gemini: usageMetadata.candidatesTokenCount / cachedContentTokenCount
	if meta, ok := obj["usageMetadata"].(map[string]any); ok {
		if compT < 0 {
			c := intFromMap(meta, "candidatesTokenCount")
			if c >= 0 {
				compT = c
				promptT = max(0, intFromMap(meta, "promptTokenCount"))
				source = "usage"
			}
		}
		cachedT = intFromMap(meta, "cachedContentTokenCount")
	}

	// OpenAI Responses API: response.completed → response.usage
	if compT < 0 {
		if t, _ := obj["type"].(string); t == "response.completed" {
			if r, ok := obj["response"].(map[string]any); ok {
				if usage, ok := r["usage"].(map[string]any); ok {
					c := intFromMap(usage, "output_tokens")
					if c >= 0 {
						compT = c
						promptT = max(0, intFromMap(usage, "input_tokens"))
						source = "usage"
					}
					// OpenAI Responses API: usage.input_tokens_details.cached_tokens
					if details, ok := usage["input_tokens_details"].(map[string]any); ok {
						cachedT = intFromMap(details, "cached_tokens")
					}
				}
			}
		}
	}

	// 收集文本片段（用于 token 估算回退）
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if delta, ok := c["delta"].(map[string]any); ok {
				if content, _ := delta["content"].(string); content != "" {
					*textParts = append(*textParts, content)
				}
			}
		}
	}

	if delta, ok := obj["delta"].(map[string]any); ok {
		if text, _ := delta["text"].(string); text != "" {
			*textParts = append(*textParts, text)
		}
	}

	if candidates, ok := obj["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					for _, p := range parts {
						if part, ok := p.(map[string]any); ok {
							if text, _ := part["text"].(string); text != "" {
								*textParts = append(*textParts, text)
							}
						}
					}
				}
			}
		}
	}

	// OpenAI Responses API: response.output_text.delta
	if t, _ := obj["type"].(string); t == "response.output_text.delta" {
		if delta, _ := obj["delta"].(string); delta != "" {
			*textParts = append(*textParts, delta)
		}
	}

	return
}

// intFromMap 按顺序查找 keys，返回第一个非负整数，否则返回 -1。
func intFromMap(m map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case float64:
			return int(x)
		case int:
			return x
		}
	}
	return -1
}
