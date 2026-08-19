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

// BenchmarkResult 包含单个问题的测试结果
type BenchmarkResult struct {
	QuestionIndex     int                                   `json:"question_index"`
	Dataset           string                                `json:"dataset"`
	Question          string                                `json:"question"`
	ExpectedAnswer    *string                               `json:"expected_answer,omitempty"` // 标准答案
	ModelAnswer       string                                `json:"model_answer"`              // 模型的完整回答
	ExtractedAnswer   string                                `json:"extracted_answer"`          // 从回答中提取的答案
	IsCorrect         *bool                                 `json:"is_correct,omitempty"`      // 答案是否正确（如果有标准答案）
	FinishReason      string                                `json:"finish_reason,omitempty"`   // 完成原因：stop, length, null 等
	TTFT              time.Duration                         `json:"ttft_ms"`                   // Time To First Token (ms)
	TotalTime         time.Duration                         `json:"total_time_ms"`             // 总用时 (ms)
	TokensUsed        int                                   `json:"tokens_used"`               // 生成的 token 数
	TPS               float64                               `json:"tps"`                       // Tokens Per Second
	TPM               float64                               `json:"tpm"`                       // Tokens Per Minute
	Error             string                                `json:"error,omitempty"`           // 错误信息
	RawRequest        *openai.ChatCompletionRequest         `json:"-"`                         // 原始请求
	RawResponseHeader http.Header                           `json:"-"`                         // 原始响应头
	RawResponse       []openai.ChatCompletionStreamResponse `json:"-"`                         // 原始响应
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
	TPS             float64 `json:"tps"`
	TPM             float64 `json:"tpm"`
	Error           string  `json:"error,omitempty"`
}
