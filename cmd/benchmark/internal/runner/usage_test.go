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
		wantChunks   int
	}{
		{
			name:         "usage chunk choices 为空，不计入内容 chunk",
			rawResponses: []openai.ChatCompletionStreamResponse{usageChunk},
			wantUsage:    usageChunk.Usage,
			wantChunks:   0,
		},
		{
			name:         "普通 chunk Usage 为 nil，不影响计数",
			rawResponses: []openai.ChatCompletionStreamResponse{contentChunk, contentChunk},
			wantUsage:    nil,
			wantChunks:   2,
		},
		{
			name:         "多 chunk 后取最后一个非空 usage",
			rawResponses: []openai.ChatCompletionStreamResponse{contentChunk, usageChunk},
			wantUsage:    usageChunk.Usage,
			wantChunks:   1,
		},
		{
			name: "usage 为 null 的普通 chunk 取下一个 usage",
			rawResponses: []openai.ChatCompletionStreamResponse{
				{Choices: []openai.ChatCompletionStreamChoice{{}}, Usage: nil},
				usageChunk,
			},
			wantUsage:  usageChunk.Usage,
			wantChunks: 1, // usage chunk 的 choices 为空不计入
		},
		{
			name:         "无 usage 回退 chunkCount",
			rawResponses: nil,
			wantUsage:    nil,
			wantChunks:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUsage, gotChunks := collectUsage(tt.rawResponses)
			if gotUsage != tt.wantUsage {
				t.Errorf("collectUsage usage = %v, want %v", gotUsage, tt.wantUsage)
			}
			if gotChunks != tt.wantChunks {
				t.Errorf("collectUsage chunkCount = %d, want %d", gotChunks, tt.wantChunks)
			}
		})
	}
}
