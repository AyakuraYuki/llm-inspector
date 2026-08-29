// Package params 定义协议无关的模型调用参数与结果类型。
//
// 类型源自 cmd/evaluation 的 provider 包（原 provider.Request/Result 等），
// 现下沉为三个工具共享的归一化层：evaluation 已完整使用；
// benchmark 通过 fork 库的参数通道对齐语义（见各工具 README 的参数映射总表）；
// performance 未来可复用 Request 以扩展参数面。
package params

import "time"

// Message 是一条对话消息。
// role=assistant 时可携带 ToolCalls（工具调用回传场景）；
// role=tool 时 Content 为工具执行结果，ToolCallID 关联此前的调用，
// Name 为函数名（Gemini 的 functionResponse 需要函数名而非 ID）。
type Message struct {
	Role       string     `json:"role" yaml:"role"`
	Content    string     `json:"content" yaml:"content"`
	Name       string     `json:"name,omitempty" yaml:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty" yaml:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`
}

// Tool 描述一个可供模型调用的函数。
type Tool struct {
	Parameters  map[string]any
	Name        string
	Description string
}

// ToolCall 是模型返回的一次工具调用。
// ID 用于把工具结果回传给模型（Gemini 无 ID 概念，用函数名代替）。
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// JSONSchemaSpec 描述 response_format 的 json_schema 模式。
// openai 走原生 response_format；gemini 走 generationConfig.responseSchema；
// anthropic 无原生支持，由检查项走 prompt 诱导。
type JSONSchemaSpec struct {
	Schema map[string]any
	Name   string
	Strict bool
}

// Request 是一次聊天补全请求（协议无关）。
// 指针类型的字段为 nil 时表示"不传该参数"，由服务端使用默认值。
//
// 输出上限语义（与 benchmark/performance 对齐，见各工具 README）：
// MaxTokens 是跨协议通用输出上限（anthropic max_tokens 必填 / gemini
// maxOutputTokens / openai max_tokens）；MaxCompletionTokens 是 openai
// 专属覆盖字段，设置时优先映射到 max_completion_tokens。
type Request struct {
	Temperature         *float64        // 温度
	TopP                *float64        // top_p 核采样，[0.0, 1.0] 之间
	FrequencyPenalty    *float64        // FrequencyPenalty 频率惩罚，仅 openai 支持
	PresencePenalty     *float64        // PresencePenalty 存在惩罚，仅 openai 支持
	Seed                *int64          // Seed 采样种子。openai/gemini 支持，anthropic 忽略。
	JSONSchema          *JSONSchemaSpec // JSONSchema 非 nil 时优先于 JSONMode。
	ParallelToolCalls   *bool           // ParallelToolCalls 是 openai 的并行工具调用开关（仅 openai 显式传参，anthropic/gemini 原生支持多工具调用块，无需参数）。
	StreamIncludeUsage  *bool           // StreamIncludeUsage 控制 openai 的 stream_options.include_usage；nil 时默认 true（保持既有行为）。anthropic/gemini 的流式恒携带 usage。
	ExtraParams         map[string]any  // ExtraParams 厂商特有参数（如 thinking、do_sample、clear_thinking）。openai/anthropic 合并到请求体顶层；gemini 合并到 generationConfig。
	Model               string          // 为空则用 Provider 默认模型
	ToolsChoice         string          // ToolsChoice 工具调用策略：""/"auto" 由模型决定；"any"/"required" 强制调用一次。
	ReasoningEffort     string          // ReasoningEffort 思考力度（openai 的 reasoning_effort，仅 openai）。
	Messages            []Message       // 消息
	Stop                []string        // Stop 停止词。openai=stop / anthropic=stop_sequences / gemini=stopSequences。
	Tools               []Tool          // 工具调用
	MaxTokens           int             // <=0 时省略（Anthropic 协议要求必填，缺省补 1024）
	MaxCompletionTokens int             // MaxCompletionTokens 是 openai 的 max_completion_tokens 兼容字段（仅 openai）。
	RequestTimeout      time.Duration   // 本次请求的超时覆盖；>0 时覆盖客户端默认超时（如长输出探测需要更长的观测窗口）。
	JSONMode            bool            // 开启 JSON 输出
}

// Result 是一次调用的统一结果，流式与非流式共用。
// FinishReason 统一映射为 OpenAI 风格：stop / length / tool_calls。
type Result struct {
	Content string
	// ReasoningContent 思考内容（openai 方言的 reasoning_content /
	// anthropic thinking 块 / gemini thought part），无思考输出时为空。
	ReasoningContent string
	FinishReason     string
	ToolCalls        []ToolCall
	// PromptTokens 全量输入 token 数。各协议统一为「含缓存」口径：openai 的
	// prompt_tokens、gemini 的 promptTokenCount 本身含缓存命中部分；anthropic 的
	// input_tokens 不含缓存读/写 token，provider 已补回 cache_read/cache_creation。
	PromptTokens     int64
	CompletionTokens int64
	// CachedInputTokens 缓存命中的输入 token 数（openai 的
	// prompt_tokens_details.cached_tokens / anthropic 的
	// cache_read_input_tokens / gemini 的 cachedContentTokenCount），
	// provider 未上报时为 0。
	CachedInputTokens int64
	Chunks            int     // 流式时的 SSE 事件数
	TTFTMS            float64 // 流式时为首个有内容 chunk（含思考内容）到达延迟；未捕获到任何内容时为 -1
	LatencyMS         float64
}
