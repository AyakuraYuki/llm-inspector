package types

import (
	"net/http"
	"time"

	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

// Question 表示一个问题及其答案
type Question struct {
	Dataset  string  `json:"dataset" yaml:"dataset"`
	Question string  `json:"question" yaml:"question"`
	Answer   *string `json:"answer" yaml:"answer"` // 可能为 null
}

// BenchmarkResult 包含单个问题的测试结果（仅在内存中使用，落盘序列化走 SerializableResult）
type BenchmarkResult struct {
	QuestionIndex         int
	Dataset               string
	Question              string
	ExpectedAnswer        *string // 标准答案
	ModelAnswer           string  // 模型的完整回答
	ExtractedAnswer       string  // 从回答中提取的答案
	IsCorrect             *bool   // 答案是否正确（如果有标准答案）
	FinishReason          string  // 完成原因：stop, length, null 等
	TTFT                  time.Duration
	TotalTime             time.Duration
	TokensUsed            int                                   // 生成的 token 数
	TokensEstimated       bool                                  // TokensUsed 来自文本估算（网关未上报 usage），可信度低于精确上报
	PromptTokens          int                                   // 输入 token 数（usage 上报；网关不支持 include_usage 时为 0）
	CachedTokens          int                                   // 缓存命中的输入 token 数
	ReasoningTokens       int                                   // 思考 token 数（usage.completion_tokens_details.reasoning_tokens）
	ReasoningTokensMerged bool                                  // reasoning_tokens 被网关当作独立于 completion_tokens 的计数上报（reasoning_tokens > completion_tokens），已合并进 TokensUsed
	TPSE2E                float64                               // 端到端 Tokens Per Second（tokens / 全程耗时，用户感知速度，含 TTFT 摊薄）
	TPME2E                float64                               // 端到端 Tokens Per Minute
	TPSDecode             float64                               // 解码 Tokens Per Second（tokens / 生成窗口），仅 DecodeValid 时有效
	TPMDecode             float64                               // 解码 Tokens Per Minute，仅 DecodeValid 时有效
	DecodeValid           bool                                  // 解码速率样本是否通过有效性校验（生成窗口双门槛 + 单流物理天花板）
	Error                 string                                // 错误信息
	RawRequest            *openai.ChatCompletionRequest         // 原始请求
	RawResponseHeader     http.Header                           // 原始响应头
	RawResponse           []openai.ChatCompletionStreamResponse // 原始响应
}

// SerializableResult 转换为可序列化的格式
type SerializableResult struct {
	Dataset               string  `json:"dataset"`
	QuestionIndex         int     `json:"question_index"`
	Question              string  `json:"question"`
	ExpectedAnswer        *string `json:"expected_answer,omitempty"`
	ModelAnswer           string  `json:"model_answer"`
	ExtractedAnswer       string  `json:"extracted_answer"`
	IsCorrect             *bool   `json:"is_correct,omitempty"`
	FinishReason          string  `json:"finish_reason,omitempty"`
	TTFTMs                int64   `json:"ttft_ms"`
	TotalTimeMs           int64   `json:"total_time_ms"`
	TokensUsed            int     `json:"tokens_used"`
	TokensEstimated       bool    `json:"tokens_estimated,omitempty"`
	PromptTokens          int     `json:"prompt_tokens,omitempty"`
	CachedTokens          int     `json:"cached_tokens,omitempty"`
	ReasoningTokens       int     `json:"reasoning_tokens,omitempty"`
	ReasoningTokensMerged bool    `json:"reasoning_tokens_merged,omitempty"`
	TPSE2E                float64 `json:"tps_e2e"`
	TPME2E                float64 `json:"tpm_e2e"`
	TPSDecode             float64 `json:"tps_decode,omitempty"`   // 仅 decode_valid 时有值
	TPMDecode             float64 `json:"tpm_decode,omitempty"`   // 仅 decode_valid 时有值
	DecodeValid           bool    `json:"decode_valid,omitempty"` // 解码速率样本是否通过有效性校验
	Error                 string  `json:"error,omitempty"`
}
