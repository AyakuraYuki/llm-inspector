package runner

import (
	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

// collectUsage 从流式响应中提取最后一个非空 usage。
//
// usage chunk 的 choices 为空（协议标准形态），不产生输出内容。
// 网关不支持 stream_options.include_usage 时返回 nil，调用方按
// 响应文本（正文 + 思考内容）估算 token 数。
func collectUsage(rawResponses []openai.ChatCompletionStreamResponse) (lastUsage *openai.Usage) {
	for i := range rawResponses {
		if rawResponses[i].Usage != nil {
			lastUsage = rawResponses[i].Usage
		}
	}
	return lastUsage
}
