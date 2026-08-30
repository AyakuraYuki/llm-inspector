package runner

import (
	"testing"

	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

func TestCollectUsage(t *testing.T) {
	usageChunk := openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{},
		Usage: &openai.Usage{
			PromptTokens:     12,
			CompletionTokens: 7,
			TotalTokens:      19,
		},
	}
	laterUsageChunk := openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{},
		Usage: &openai.Usage{
			PromptTokens:     12,
			CompletionTokens: 9,
			TotalTokens:      21,
		},
	}
	contentChunk := openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{
			{Delta: openai.ChatCompletionStreamChoiceDelta{Content: "hi"}},
		},
		Usage: nil,
	}

	tests := []struct {
		name         string
		rawResponses []openai.ChatCompletionStreamResponse
		wantUsage    *openai.Usage
	}{
		{
			name:         "提取唯一的 usage chunk",
			rawResponses: []openai.ChatCompletionStreamResponse{usageChunk},
			wantUsage:    usageChunk.Usage,
		},
		{
			name:         "普通 chunk Usage 为 nil，不影响提取",
			rawResponses: []openai.ChatCompletionStreamResponse{contentChunk, contentChunk},
			wantUsage:    nil,
		},
		{
			name:         "多 chunk 后取最后一个非空 usage",
			rawResponses: []openai.ChatCompletionStreamResponse{contentChunk, usageChunk, laterUsageChunk},
			wantUsage:    laterUsageChunk.Usage,
		},
		{
			name: "usage 为 null 的普通 chunk 取下一个 usage",
			rawResponses: []openai.ChatCompletionStreamResponse{
				{Choices: []openai.ChatCompletionStreamChoice{{}}, Usage: nil},
				usageChunk,
			},
			wantUsage: usageChunk.Usage,
		},
		{
			name:         "无 usage 返回 nil，调用方走文本估算",
			rawResponses: nil,
			wantUsage:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotUsage := collectUsage(tt.rawResponses); gotUsage != tt.wantUsage {
				t.Errorf("collectUsage usage = %v, want %v", gotUsage, tt.wantUsage)
			}
		})
	}
}
