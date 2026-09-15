package types

import (
	"math/rand/v2"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/prompts"
)

// DefaultMaxOutputTokens 是各协议输出上限的默认取值。压测对比的是服务性能，
// 若输出长度不受控（模型想写多长写多长），E2E 时延/TPOT/TPS 会混入生成长度的
// 自然波动，同一模型的分位数失真，跨协议横向对比也不公平。
const DefaultMaxOutputTokens = 8192

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
	OutputEstimated   bool          // OutputTokens 来自文本估算（provider 未上报 usage），可信度低于精确上报
	CachedInputTokens int64         // 命中缓存的输入 token 数（provider 未上报缓存字段时为 0）
	CacheReported     bool          // provider 是否上报了缓存命中字段（区分「未上报」与「上报了但命中为 0」）
	ITLSamplesMS      []float64     // 逐次输出内容事件之间的间隔（毫秒），按 SSE 事件粒度近似逐 token 生成间隔（ITL），仅成功的流式请求非空
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

	// ThinkTime 是 closed-loop 下同一 worker 两次请求之间的等待（拟人思考时间）。
	// 默认 300ms（历史行为），设为 0 等价于 evalscope `--rate -1` 的"完成即发"语义，
	// 是和它对拍吞吐数字的前置条件——同样的并发数下，思考时间不同则实际负载不同。
	// 零值按 300ms 处理会让"显式配 0"无法表达，因此这里的 0 就是 0；
	// 直接构造 BenchmarkConfig 的调用方（测试）因此默认不等待。
	// open-loop 不使用该字段（到达节奏由 RequestRate 的泊松过程决定）。
	ThinkTime time.Duration

	// MaxOutputTokens 是各协议输出长度上限（max_tokens/maxOutputTokens/
	// max_output_tokens）的统一取值，<=0 时回退到 DefaultMaxOutputTokens。
	// 可配置是为了对齐 evalscope 的输出长度控制，默认值仍固定，保持跨协议可比。
	MaxOutputTokens int

	// 错误率早停与跳档，EarlyStopEnabled 为 false 时以下字段无效
	EarlyStopEnabled      bool
	MaxErrorRate          float64 // 档位失败率超过该值判定为不可用，(0,1]
	MinSamples            int     // 至少凑够这么多请求才评估错误率
	SkipHigherConcurrency bool    // 判定不可用时是否跳过该模型剩余的更高并发档位

	// open-loop 目标 RPS 模式，OpenLoop 为 false（默认）时以下字段无效，
	// 行为与现有 closed-loop 完全一致。开启后 RequestRate 与 Concurrency
	// 一一对应：RequestRate[i] 是该档位的目标 RPS（泊松到达），
	// Concurrency[i] 变为该档位同时在途请求数上限。
	OpenLoop    bool
	RequestRate []float64

	// SLO 达标率（goodput）判定阈值，SLO.Enabled() 为 false 时不计算 goodput。
	SLO SLOThresholds

	// ShowHistogram 为真时额外计算延迟分布直方图（E2E/TTFT），默认 false 不计算。
	ShowHistogram bool
}

// SLOThresholds 定义 goodput 判定用的 SLO 阈值，三项均可选。
// 零值表示该维度不参与达标判定；Enabled 为 false（全部为零）时整个 goodput
// 计算被跳过，报表不产生任何新内容。
type SLOThresholds struct {
	TTFT time.Duration // 首 token 时延阈值，0 表示不参与判定
	TPOT time.Duration // 每 token 生成耗时阈值，0 表示不参与判定
	E2E  time.Duration // 端到端时延阈值，0 表示不参与判定
}

// Enabled 判断是否至少配置了一项 SLO 阈值。
func (s SLOThresholds) Enabled() bool {
	return s.TTFT > 0 || s.TPOT > 0 || s.E2E > 0
}

// HistBucket 是延迟分布直方图的一个分桶：[Lo, Hi) 区间内的样本数。
type HistBucket struct {
	Lo    time.Duration
	Hi    time.Duration // 最后一个桶 Hi 为该桶下限之上的所有样本（无上界）
	Count int
}

// EffectiveMaxOutputTokens 返回本次压测实际使用的输出长度上限，
// 未配置（<=0）时回退到 DefaultMaxOutputTokens。
func (c BenchmarkConfig) EffectiveMaxOutputTokens() int {
	if c.MaxOutputTokens > 0 {
		return c.MaxOutputTokens
	}
	return DefaultMaxOutputTokens
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
	Model        string
	Provider     Provider
	TokenGroup   string
	Concurrency  int
	TargetRate   float64       // open-loop 档位的目标 RPS，closed-loop 档位为 0
	Start        time.Time     // 档位开始时刻
	Window       time.Duration // 名义压测时长（吞吐统计窗口的上限）
	Elapsed      time.Duration // 实际运行时长（含 deadline 后在途请求的排空期）
	Metrics      []RequestMetrics
	StoppedEarly bool // 是否因错误率超过 EarlyStop 阈值被提前终止（而非到达 deadline 或用户中止）
}

// PercentileStats 保存时延类指标的分位数统计。
// 低分位（P10/P25/P75/P90）与 Min/Max/StdDev 用于和 evalscope 的十分位表对标：
// 只报高分位时看不出分布是"整体偏慢"还是"少数长尾拖高均值"，StdDev 则一眼
// 区分稳定服务与抖动服务。终端只渲染其中最常看的几列，全量在 Excel/JSON 里。
type PercentileStats struct {
	Min    time.Duration
	P10    time.Duration
	P25    time.Duration
	P50    time.Duration
	P75    time.Duration
	P90    time.Duration
	P95    time.Duration
	P99    time.Duration
	P995   time.Duration
	P999   time.Duration
	Max    time.Duration
	Avg    time.Duration
	StdDev time.Duration // 总体标准差（分母 N，非样本标准差）
	N      int
}

// FloatStats 保存 float64 指标的分位数统计（如 TPS、TPM 等非时延指标）。
// 分位档位与 PercentileStats 保持一致，便于两类指标在报表里同构渲染。
type FloatStats struct {
	Min    float64
	P10    float64
	P25    float64
	P50    float64
	P75    float64
	P90    float64
	P95    float64
	P99    float64
	P995   float64
	P999   float64
	Max    float64
	Avg    float64
	StdDev float64 // 总体标准差（分母 N，非样本标准差）
	N      int
}

// AggregatedMetrics 保存一次测试的汇聚指标
type AggregatedMetrics struct {
	Model         string
	Provider      Provider
	TokenGroup    string
	Concurrency   int
	TargetRate    float64       // open-loop 档位的目标 RPS，closed-loop 档位为 0
	Start         time.Time     // 档位开始时刻，用于报表排查时段性波动
	Elapsed       time.Duration // 实际运行时长（含 deadline 后在途请求的排空期）
	Window        time.Duration // 吞吐统计窗口（正常档位为名义压测时长，中止档位为实际运行时长）
	Total         int
	Success       int
	Failed        int
	ErrorCounts   map[ErrorType]int
	FailedDetails []RequestMetrics // 每条失败请求的原始记录，用于错误明细 sheet
	StoppedEarly  bool             // 是否因错误率超过 EarlyStop 阈值被提前终止

	// 仅流式端点有效
	TTFT             PercentileStats // 首 token 时延，仅统计成功请求
	TPOT             PercentileStats // Time Per Output Token（gen_window / output_tokens）
	ITL              PercentileStats // 逐输出内容事件的间隔（按 SSE 事件粒度近似的逐 token 生成间隔），剔除口径与 TPOT/TPS 一致
	TpsPr            FloatStats      // per-request tokens/s 分位数
	TpmPr            FloatStats      // per-request tokens/min 分位数
	GenSpeedExcluded int             // 未通过有效性校验（生成窗口过窄或超出单流物理天花板，测不出真实解码速度）被 TPOT/TPS/TPM/ITL 剔除的成功样本数
	EstimatedOutputs int             // OutputTokens 来自文本估算（无 usage 上报）的成功样本数；占比高时速率分位数可信度下降
	IOR              FloatStats      // per-request 输出/输入 token 比（output_tokens / input_tokens）分位数
	CacheHitPr       FloatStats      // per-request 缓存命中率（cached_input_tokens / input_tokens * 100）分位数，仅上报了缓存字段的请求入样

	// 所有端点均有
	Latency PercentileStats // 端到端时延，仅统计成功请求（失败时延见 FailedDetails）

	// 系统级吞吐量（吞吐窗口内完成的总量 / Window）
	TPS float64
	TPM float64
	QPS float64
	QPM float64

	// DecodeTPS 是单流解码速度（tok/s），由平均 TPOT 取倒数得到，对标 evalscope
	// 的 Decode tok/s。与 TpsPr.Avg 同源但口径不同：TpsPr 是各请求速率的均值，
	// DecodeTPS 是平均每 token 耗时的倒数（调和均值口径），长短响应混跑时前者
	// 被短响应拉高，后者更贴近"每个 token 平均要等多久"。TPOT 无样本时为 0。
	DecodeTPS float64

	// 系统级输入/输出 token 比（总 output_tokens / 总 input_tokens，仅统计有 usage 上报的请求）
	IORatio float64

	// 系统级缓存命中统计（原始 token 总量 + 命中率，仅统计有 usage 上报的请求）
	TotalInputTokens   int64
	TotalCachedTokens  int64
	CacheReportedCount int     // 上报了缓存命中字段的成功请求数；为 0 时缓存命中率无意义，报表显示 N/A
	CacheHitRatio      float64 // TotalCachedTokens / TotalInputTokens * 100(%)

	// SLO 达标率（goodput），SLOConfigured 为 false 时以下三项无意义，报表显示 N/A
	SLOConfigured bool
	SLO           SLOThresholds // 判定用的阈值原样保留，供报表渲染摘要文案
	GoodputCount  int
	GoodputRatio  float64 // GoodputCount / Total * 100(%)

	// 延迟分布直方图，仅 ShowHistogram 开启时计算，否则为空
	E2EHistogram  []HistBucket
	TTFTHistogram []HistBucket
}
