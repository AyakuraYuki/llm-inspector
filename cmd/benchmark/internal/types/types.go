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
	QuestionIndex     int
	Dataset           string
	Question          string
	ExpectedAnswer    *string // 标准答案
	ModelAnswer       string  // 模型的完整回答
	ExtractedAnswer   string  // 从回答中提取的答案
	IsCorrect         *bool   // 答案是否正确（如果有标准答案）
	FinishReason      string  // 完成原因：stop, length, null 等
	TTFT              time.Duration
	TotalTime         time.Duration
	TokensUsed        int                                   // 生成的 token 数
	PromptTokens      int                                   // 输入 token 数（usage 上报；网关不支持 include_usage 时为 0）
	CachedTokens      int                                   // 缓存命中的输入 token 数
	ReasoningTokens   int                                   // 思考 token 数（usage.completion_tokens_details.reasoning_tokens）
	TPS               float64                               // Tokens Per Second
	TPM               float64                               // Tokens Per Minute
	Error             string                                // 错误信息
	RawRequest        *openai.ChatCompletionRequest         // 原始请求
	RawResponseHeader http.Header                           // 原始响应头
	RawResponse       []openai.ChatCompletionStreamResponse // 原始响应
}

// SerializableResult 转换为可序列化的格式
type SerializableResult struct {
	Dataset         string  `json:"dataset"`
	QuestionIndex   int     `json:"question_index"`
	Question        string  `json:"question"`
	ExpectedAnswer  *string `json:"expected_answer,omitempty"`
	ModelAnswer     string  `json:"model_answer"`
	ExtractedAnswer string  `json:"extracted_answer"`
	IsCorrect       *bool   `json:"is_correct,omitempty"`
	FinishReason    string  `json:"finish_reason,omitempty"`
	TTFTMs          int64   `json:"ttft_ms"`
	TotalTimeMs     int64   `json:"total_time_ms"`
	TokensUsed      int     `json:"tokens_used"`
	PromptTokens    int     `json:"prompt_tokens,omitempty"`
	CachedTokens    int     `json:"cached_tokens,omitempty"`
	ReasoningTokens int     `json:"reasoning_tokens,omitempty"`
	TPS             float64 `json:"tps"`
	TPM             float64 `json:"tpm"`
	Error           string  `json:"error,omitempty"`
}
