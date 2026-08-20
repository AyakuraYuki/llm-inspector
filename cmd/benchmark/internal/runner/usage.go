package runner

import (
	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

// collectUsage 从流式响应中提取最后一个非空 usage 与内容 chunk 计数。
//
// usage chunk 的 choices 为空（协议标准形态），不产生输出内容；
// chunkCount 统计携带 choices 的内容 chunk 数，用于网关不支持
// stream_options.include_usage 时的 token 回退估算（保持旧行为）。
func collectUsage(rawResponses []openai.ChatCompletionStreamResponse) (lastUsage *openai.Usage, chunkCount int) {
	for i := range rawResponses {
		resp := &rawResponses[i]
		if resp.Usage != nil {
			lastUsage = resp.Usage
		}
		if len(resp.Choices) > 0 {
			chunkCount++
		}
	}
	return lastUsage, chunkCount
}
