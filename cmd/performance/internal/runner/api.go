package runner

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
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/sse"
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
// 初始仅按小容量建池，preflight/正式压测开始前会由
// configureSharedClient 按本次实际最大并发重建。
var sharedClient = &http.Client{Transport: newTransport(16)}

// configureSharedClient 按本次压测将要用到的最大并发数重建连接池。
// 应在解析完 -concurrency 参数、preflightCheck/正式压测开始之前调用一次。
func configureSharedClient(maxConcurrency int) {
	sharedClient.Transport = newTransport(maxConcurrency)
}

// classifyNetError 将 sharedClient.Do 返回的传输层错误细分为更具体的类型，
// 便于区分"我们自己的超时预算到了"“上游拒绝/重置连接”"DNS 挂了"等不同故障域。
// 判断顺序很重要：更具体的原因要排在通用的 net.Error.Timeout() 之前，
// 否则例如 DNS 超时会被笼统地归为 net_timeout 而丢失"是 DNS 的问题"这一信息。
func classifyNetError(err error) types.ErrorType {
	if errors.Is(err, context.Canceled) {
		return types.ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return types.ErrorTypeTimeout
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return types.ErrorTypeDNS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return types.ErrorTypeConnRefused
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return types.ErrorTypeConnReset
	}
	if isTLSError(err) {
		return types.ErrorTypeTLS
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return types.ErrorTypeNetTimeout
	}
	return types.ErrorTypeConnect
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
func classifyHTTPStatus(code int) types.ErrorType {
	switch {
	case code == http.StatusTooManyRequests:
		return types.ErrorTypeRateLimit
	case code >= 500:
		return types.ErrorTypeServerError
	default:
		return types.ErrorTypeHTTP
	}
}

const (
	streamTimeout = 30 * time.Minute
	imageTimeout  = 30 * time.Minute

	// maxOutputTokens 统一各协议的输出长度上限。压测对比的是服务性能，
	// 若输出长度不受控（模型想写多长写多长），E2E 时延/TPOT/TPS 会混入
	// 生成长度的自然波动，同一模型的分位数失真，跨协议横向对比也不公平。
	maxOutputTokens = 8192
)

type doSSERequest func(context.Context, types.BenchmarkConfig, types.ModelSpec) types.RequestMetrics

// reqInfo 记录一次请求的可复述上下文（方法、地址、原始请求体），供失败时写入错误日志。
type reqInfo struct {
	method  string
	url     string
	payload []byte
}

// stageOf 把错误分类映射到 errlog 的阶段标识。
func stageOf(t types.ErrorType) string {
	switch t {
	case types.ErrorTypeRateLimit, types.ErrorTypeServerError, types.ErrorTypeHTTP:
		return errlog.StageHTTPStatus
	case types.ErrorTypeStreamBroken, types.ErrorTypeStreamTruncated, types.ErrorTypeUpstreamError, types.ErrorTypeNoContent:
		return errlog.StageStream
	default:
		return errlog.StageTransport
	}
}

// logFailure 把一次失败请求写入错误日志并回填 RequestID 后原样返回指标。
// resp/respBody 允许为 nil（传输层失败拿不到响应）。压测中止批量取消的
// 在途请求（canceled）不属于服务端错误，不记录，避免中止瞬间刷屏。
func logFailure(info reqInfo, resp *http.Response, respBody []byte, m types.RequestMetrics) types.RequestMetrics {
	if m.ErrorType == types.ErrorTypeCanceled {
		return m
	}
	e := errlog.Entry{
		Stage:  stageOf(m.ErrorType),
		Method: info.method,
		URL:    errlog.RedactURLString(info.url),
		Error:  m.Error,
	}
	e.RequestBody, e.RequestBodyTruncated, e.RequestBodySize = errlog.BodyForLog(info.payload)
	if resp != nil {
		e.Status = resp.StatusCode
		e.ResponseHeaders = resp.Header
		e.RequestIDs = errlog.ExtractRequestIDs(resp.Header, respBody)
		e.ResponseBodySnippet = string(respBody)
		m.RequestID = errlog.PrimaryRequestID(e.RequestIDs)
	}
	errlog.Record(e)
	return m
}

// maxErrorBodyPeek 是错误响应体的读取上限：错误信息只保留前 512 字节（与历史
// 行为一致），但完整读到的部分都参与 RequestID 提取并进入错误日志。
const maxErrorBodyPeek = 4096

// httpStatusFailure 处理非 200 响应：构造失败指标并写入错误日志。
func httpStatusFailure(info reqInfo, t0 time.Time, resp *http.Response) types.RequestMetrics {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyPeek))
	msg := body
	if len(msg) > 512 {
		msg = msg[:512]
	}
	m := types.RequestMetrics{
		Success:      false,
		TotalLatency: time.Since(t0),
		Error:        fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg))),
		ErrorType:    classifyHTTPStatus(resp.StatusCode),
	}
	return logFailure(info, resp, body, m)
}

// doSSERequests 声明了受支持的接口协议类型，并且可根据接口协议类型获取相应的
// 流式调用方法。
var doSSERequests = map[types.Provider]doSSERequest{
	types.ProviderAnthropic:      doAnthropicRequest,
	types.ProviderGemini:         doGeminiRequest,
	types.ProviderOpenAI:         doOpenAIRequest,
	types.ProviderOpenAIImage:    doImageRequest,
	types.ProviderOpenAIResponse: doOpenAIResponseRequest,
	types.ProviderBaseline:       doBaselineRequest,
}

func IsSupportedProvider(p types.Provider) bool {
	_, ok := doSSERequests[p]
	return ok
}

func RegisteredProviders() []types.Provider {
	return slices.Sorted(maps.Keys(doSSERequests))
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
func doAnthropicRequest(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":      model.Name,
		"max_tokens": maxOutputTokens,
		"stream":     true,
		"messages":   []map[string]any{{"role": "user", "content": cfg.BuildPrompt()}},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	info := reqInfo{method: http.MethodPost, url: cfg.BaseURL + "/v1/messages", payload: payload}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, info.url, bytes.NewReader(payload))
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+model.PickToken())
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	m := parseStreamMetrics(t0, resp.Body)
	if !m.Success {
		return logFailure(info, resp, nil, m)
	}
	return m
}

// doOpenAIRequest 向 /v1/chat/completions 发起 SSE 流式请求。
func doOpenAIRequest(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	// 用 max_tokens 而非 OpenAI 新版的 max_completion_tokens：前者是
	// OpenAI 兼容生态（NewAPI 等网关及各家上游）普遍接受的字段，
	// 网关会按需为 o 系列等模型转换字段名
	payload, _ := json.Marshal(map[string]any{
		"model":          model.Name,
		"max_tokens":     maxOutputTokens,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"messages":       []map[string]any{{"role": "user", "content": cfg.BuildPrompt()}},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	info := reqInfo{method: http.MethodPost, url: cfg.BaseURL + "/v1/chat/completions", payload: payload}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, info.url, bytes.NewReader(payload))
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+model.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	m := parseStreamMetrics(t0, resp.Body)
	if !m.Success {
		return logFailure(info, resp, nil, m)
	}
	return m
}

// doGeminiRequest 向 /v1beta/models/{model}:streamGenerateContent 发起 SSE 流式请求。
func doGeminiRequest(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]any{{"text": cfg.BuildPrompt()}},
			},
		},
		"generationConfig": map[string]any{"maxOutputTokens": maxOutputTokens},
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", cfg.BaseURL, model.Name)
	info := reqInfo{method: http.MethodPost, url: url, payload: payload}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+model.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	m := parseStreamMetrics(t0, resp.Body)
	if !m.Success {
		return logFailure(info, resp, nil, m)
	}
	return m
}

// doImageRequest 向 /v1/images/generations 发起同步请求。
func doImageRequest(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":  model.Name,
		"prompt": cfg.ImagePrompt,
		"n":      1,
		"size":   "1024x1024",
	})

	reqCtx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()

	info := reqInfo{method: http.MethodPost, url: cfg.BaseURL + "/v1/images/generations", payload: payload}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, info.url, bytes.NewReader(payload))
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+model.PickToken())

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	// 读完响应体确保连接可复用
	_, _ = io.Copy(io.Discard, resp.Body)
	return types.RequestMetrics{
		TotalLatency: time.Since(t0),
		Success:      true,
	}
}

// doOpenAIResponseRequest 向 /v1/responses 发起 SSE 流式请求（OpenAI Responses API）。
func doOpenAIResponseRequest(ctx context.Context, cfg types.BenchmarkConfig, model types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	payload, _ := json.Marshal(map[string]any{
		"model":             model.Name,
		"input":             cfg.BuildPrompt(),
		"max_output_tokens": maxOutputTokens,
		"stream":            true,
	})

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	info := reqInfo{method: http.MethodPost, url: cfg.BaseURL + "/v1/responses", payload: payload}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, info.url, bytes.NewReader(payload))
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+model.PickToken())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	m := parseStreamMetrics(t0, resp.Body)
	if !m.Success {
		return logFailure(info, resp, nil, m)
	}
	return m
}

// doBaselineRequest 对完全不经过任何大模型调用的静态接口发起同步 GET 请求，
// 用来单独衡量网关/接入层本身（TLS 握手、鉴权、路由等）在各并发档位下的开销，
// 和模型推理耗时解耦。
// 请求头参照真实浏览器抓包，避免被网关按 UA/Referer/Sec-Fetch-* 拦截。
func doBaselineRequest(ctx context.Context, cfg types.BenchmarkConfig, _ types.ModelSpec) types.RequestMetrics {
	t0 := time.Now()

	reqCtx, cancel := context.WithTimeout(ctx, streamTimeout)
	defer cancel()

	info := reqInfo{method: http.MethodGet, url: cfg.BaseURL + "/api/user-agreement"}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, info.url, nil)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: types.ErrorTypeConnect})
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := sharedClient.Do(req)
	if err != nil {
		return logFailure(info, nil, nil, types.RequestMetrics{Success: false, TotalLatency: time.Since(t0), Error: err.Error(), ErrorType: classifyNetError(err)})
	}
	defer func(Body io.ReadCloser) { _ = Body.Close() }(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return httpStatusFailure(info, t0, resp)
	}

	// 读完响应体确保连接可复用
	_, _ = io.Copy(io.Discard, resp.Body)
	return types.RequestMetrics{
		TotalLatency: time.Since(t0),
		Success:      true,
	}
}

// parseStreamMetrics 消费 SSE 流，提取 TTFT / tokens / e2e。
func parseStreamMetrics(t0 time.Time, body io.Reader) types.RequestMetrics {
	var firstByteMs float64
	tracker := &firstByteTracker{r: body, t0: t0, firstByte: &firstByteMs}

	s := sse.NewStreamSummary()

	scanner := bufio.NewScanner(tracker)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if sse.IsDoneLine(line) {
			s.TerminalSeen = true
			continue
		}
		obj := sse.ParseLine(line)
		if obj == nil {
			continue
		}
		sse.ApplySSEEvent(obj, time.Since(t0).Seconds()*1000, s)
	}

	e2eMs := time.Since(t0).Seconds() * 1000
	s.FirstByteMS = firstByteMs

	// 流读取中途出错（连接被重置/网络中断等），不能当作正常结束处理，
	// 否则已收到的部分内容会让请求被误判为成功（只是输出被截断）。
	if scanErr := scanner.Err(); scanErr != nil {
		return types.RequestMetrics{
			Success:      false,
			TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
			Error:        scanErr.Error(),
			ErrorType:    types.ErrorTypeStreamBroken,
		}
	}

	// HTTP 200 建流之后网关才发现上游失败时，只能以流内错误事件收尾。
	// 这类请求的输出被截断甚至为空，若照常记成功，会虚高成功率，
	// 且偏短的 E2E/token 会拉低时延分位数、污染 TPOT/TPS 分布。
	if s.UpstreamErr != "" {
		return types.RequestMetrics{
			Success:      false,
			TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
			Error:        "upstream error event: " + s.UpstreamErr,
			ErrorType:    types.ErrorTypeUpstreamError,
		}
	}

	// 全程未解析到任何输出内容时，不能用首字节时间冒充 TTFT 把空生成记成成功：
	// 首字节往往只是 message_start/ping 之类的元数据事件，既拉低 TTFT 分位数，
	// 又让空响应混进成功率和 QPS。仅当 usage 明确报告了输出 token（内容可能是
	// 本程序未识别的事件格式）时，才回退用首字节时间近似 TTFT 并按成功处理。
	if s.TTFTMS < 0 {
		if s.UsageSeen && s.CompletionTokens > 0 && s.FirstByteMS > 0 {
			s.TTFTMS = s.FirstByteMS
		} else {
			return types.RequestMetrics{
				Success:      false,
				TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
				Error:        "stream ended without output content",
				ErrorType:    types.ErrorTypeNoContent,
			}
		}
	}

	// 流干净地 EOF 但全程未出现协议终止标记：多半是网关/上游把连接提前
	// 掐断，输出被无声截断。按成功处理会让这些偏短的请求污染分位数。
	if !s.TerminalSeen {
		return types.RequestMetrics{
			Success:      false,
			TotalLatency: time.Duration(e2eMs * float64(time.Millisecond)),
			Error:        "stream ended without terminal marker ([DONE]/finish_reason/message_stop/finishReason/response.completed)",
			ErrorType:    types.ErrorTypeStreamTruncated,
		}
	}

	// 无 usage 时用字符数粗估
	if !s.UsageSeen && len(s.TextParts) > 0 {
		joined := strings.Join(s.TextParts, "")
		if strings.TrimSpace(joined) != "" {
			s.CompletionTokens = max(int64(1), int64(len(joined)/4))
		}
	}

	return types.RequestMetrics{
		TTFT:              time.Duration(s.TTFTMS * float64(time.Millisecond)),
		TotalLatency:      time.Duration(e2eMs * float64(time.Millisecond)),
		InputTokens:       s.PromptTokens,
		OutputTokens:      s.CompletionTokens,
		CachedInputTokens: max(int64(0), s.CachedInputTokens),
		Success:           true,
	}
}
