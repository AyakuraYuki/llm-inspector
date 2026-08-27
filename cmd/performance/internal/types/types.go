package types

import (
	"math/rand/v2"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/prompts"
)

// ErrorType 分类请求失败原因
type ErrorType string

const (
	ErrorTypeNone            ErrorType = ""
	ErrorTypeTimeout         ErrorType = "timeout"          // 请求超时（context deadline）
	ErrorTypeNetTimeout      ErrorType = "net_timeout"      // 网络层超时（net.Error.Timeout()，排除下面几类更具体的原因）
	ErrorTypeCanceled        ErrorType = "canceled"         // 请求被取消（context.Canceled，通常是压测中止）
	ErrorTypeDNS             ErrorType = "dns_error"        // DNS 解析失败
	ErrorTypeConnRefused     ErrorType = "conn_refused"     // 连接被拒绝（端口未监听/服务下线）
	ErrorTypeConnReset       ErrorType = "conn_reset"       // 连接被对端重置/broken pipe（可能发生在发送或读取阶段）
	ErrorTypeTLS             ErrorType = "tls_error"        // TLS 握手/证书错误
	ErrorTypeConnect         ErrorType = "connect"          // 其他拨号失败（兜底分类）
	ErrorTypeRateLimit       ErrorType = "rate_limited"     // HTTP 429
	ErrorTypeServerError     ErrorType = "server_error"     // HTTP 5xx
	ErrorTypeHTTP            ErrorType = "http_error"       // 其他非 200 HTTP 状态码（如 4xx）
	ErrorTypeStreamBroken    ErrorType = "stream_broken"    // 流读取中途中断（scanner 报错，非正常结束）
	ErrorTypeStreamTruncated ErrorType = "stream_truncated" // 流正常 EOF 但未出现协议终止标记（[DONE]/message_stop/finish_reason 等），疑似被上游/网关无声截断
	ErrorTypeUpstreamError   ErrorType = "upstream_error"   // HTTP 200 建流后收到流内错误事件（网关转发上游失败的常见形态）
	ErrorTypeNoContent       ErrorType = "no_content"       // 流式响应正常结束但无可读内容
)

// ErrorTypeOrder 定义错误类型在报表/进度展示中的固定顺序。
var ErrorTypeOrder = []ErrorType{
	ErrorTypeTimeout, ErrorTypeNetTimeout, ErrorTypeCanceled, ErrorTypeDNS,
	ErrorTypeConnRefused, ErrorTypeConnReset, ErrorTypeTLS, ErrorTypeConnect,
	ErrorTypeRateLimit, ErrorTypeServerError, ErrorTypeHTTP, ErrorTypeUpstreamError,
	ErrorTypeStreamBroken, ErrorTypeStreamTruncated, ErrorTypeNoContent,
}

// Provider 标识接口协议类型
type Provider string

const (
	ProviderAnthropic      Provider = "anthropic"
	ProviderGemini         Provider = "gemini"
	ProviderOpenAI         Provider = "openai"
	ProviderOpenAIImage    Provider = "openai-image"
	ProviderOpenAIResponse Provider = "openai-response"
	ProviderBaseline       Provider = "__baseline__"
)

// ModelSpec 保存模型名称和协议类型
type ModelSpec struct {
	Name       string
	Provider   Provider
	TokenGroup string
	Tokens     []string
}

// RequestMetrics 记录单次请求的原始指标
type RequestMetrics struct {
	Timestamp         time.Time     // 请求发起时刻，用于吞吐窗口判定和错误明细排查
	TTFT              time.Duration // 首 token 时延（图片生成为 0）
	TotalLatency      time.Duration // 端到端总时延
	InputTokens       int64         // 输入 token 数（图片生成为 0，无 usage 上报时为 0）
	OutputTokens      int64         // 输出 token 数（图片生成为 0）
	CachedInputTokens int64         // 命中缓存的输入 token 数（provider 未上报缓存字段时为 0）
	Success           bool
	Error             string
	ErrorType         ErrorType
	RequestID         string // 失败请求从响应 Header/响应体提取的请求 ID（拿不到响应时为空）
}

// BenchmarkConfig 保存测试参数
type BenchmarkConfig struct {
	BaseURL          string
	Models           []ModelSpec
	Concurrency      []int
	Duration         time.Duration
	Prompt           string
	ImagePrompt      string
	DynamicPrompt    bool // 开启后，每次文本请求都会现场拼装一段随机长文本，替代固定的 Prompt
	PromptTokens     int  // DynamicPrompt 生成文本的目标近似 token 数
	CodexPrompt      bool // 开启后，用类 Codex 系统提示词 + 随机简短提问替代 Prompt，模拟高相似度请求场景；与 DynamicPrompt、Prompt 互斥
	Warmup           bool
	WarmupDuration   time.Duration
	CooldownDuration time.Duration
}

// PickToken 从该模型关联的 token 分组中随机返回一个 token。
func (m ModelSpec) PickToken() string {
	l := len(m.Tokens)
	if l == 1 {
		return m.Tokens[0] // 快速返回
	}
	return m.Tokens[rand.IntN(l)]
}

// BuildPrompt 返回本次文本请求使用的 prompt。DynamicPrompt、CodexPrompt、Prompt
// 三者互斥：DynamicPrompt 开启时，每次调用都会现场拼装一段目标长度的随机长文本
// （用于长上下文压测）；CodexPrompt 开启时，返回类 Codex 系统提示词加随机简短提问
// （用于高相似度请求压测）；否则返回固定的 Prompt。
func (c *BenchmarkConfig) BuildPrompt() string {
	switch {
	case c.DynamicPrompt:
		return prompts.BuildDynamicPrompt(c.PromptTokens)
	case c.CodexPrompt:
		return prompts.BuildCodexPrompt()
	default:
		return c.Prompt
	}
}

// BenchmarkResult 保存一个 model×concurrency 组合的原始测试结果
type BenchmarkResult struct {
	Model       string
	Provider    Provider
	TokenGroup  string
	Concurrency int
	Start       time.Time     // 档位开始时刻
	Window      time.Duration // 名义压测时长（吞吐统计窗口的上限）
	Elapsed     time.Duration // 实际运行时长（含 deadline 后在途请求的排空期）
	Metrics     []RequestMetrics
}

// PercentileStats 保存时延类指标的分位数统计
type PercentileStats struct {
	P50  time.Duration
	P95  time.Duration
	P99  time.Duration
	P995 time.Duration
	P999 time.Duration
	Avg  time.Duration
	Min  time.Duration
	Max  time.Duration
	N    int
}

// FloatStats 保存 float64 指标的分位数统计（如 TPS、TPM 等非时延指标）
type FloatStats struct {
	P50  float64
	P95  float64
	P99  float64
	P995 float64
	P999 float64
	Avg  float64
	N    int
}

// AggregatedMetrics 保存一次测试的汇聚指标
type AggregatedMetrics struct {
	Model         string
	Provider      Provider
	TokenGroup    string
	Concurrency   int
	Start         time.Time     // 档位开始时刻，用于报表排查时段性波动
	Elapsed       time.Duration // 实际运行时长（含 deadline 后在途请求的排空期）
	Window        time.Duration // 吞吐统计窗口（正常档位为名义压测时长，中止档位为实际运行时长）
	Total         int
	Success       int
	Failed        int
	ErrorCounts   map[ErrorType]int
	FailedDetails []RequestMetrics // 每条失败请求的原始记录，用于错误明细 sheet

	// 仅流式端点有效
	TTFT             PercentileStats // 首 token 时延，仅统计成功请求
	TPOT             PercentileStats // Time Per Output Token（gen_window / output_tokens）
	TpsPr            FloatStats      // per-request tokens/s 分位数
	TpmPr            FloatStats      // per-request tokens/min 分位数
	GenSpeedExcluded int             // 因生成窗口过窄（响应一次性到达，测不出真实解码速度）被 TPOT/TPS/TPM 剔除的成功样本数
	IOR              FloatStats      // per-request 输出/输入 token 比（output_tokens / input_tokens）分位数
	CacheHitPr       FloatStats      // per-request 缓存命中率（cached_input_tokens / input_tokens * 100）分位数，仅上报了缓存字段的 provider 有效

	// 所有端点均有
	Latency PercentileStats // 端到端时延，仅统计成功请求（失败时延见 FailedDetails）

	// 系统级吞吐量（吞吐窗口内完成的总量 / Window）
	TPS float64
	TPM float64
	QPS float64
	QPM float64

	// 系统级输入/输出 token 比（总 output_tokens / 总 input_tokens，仅统计有 usage 上报的请求）
	IORatio float64

	// 系统级缓存命中统计（原始 token 总量 + 命中率，仅统计有 usage 上报的请求）
	TotalInputTokens  int64
	TotalCachedTokens int64
	CacheHitRatio     float64 // TotalCachedTokens / TotalInputTokens * 100(%)
}
