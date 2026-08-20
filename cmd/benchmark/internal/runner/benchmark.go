package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

const TimeoutPerRequest = 30 * time.Minute

func RunBenchmark(client *openai.Client, model string, questions []types.Question, benchmarkCfg config.BenchmarkConfig) []types.BenchmarkResult {
	results := make([]types.BenchmarkResult, len(questions))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, benchmarkCfg.MaxWorkers)

	// 启动心跳监控器，定期输出整体进度和正在执行的测试项目
	tracker := reporter.NewProgressTracker(len(questions))
	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	go tracker.Monitor(monitorCtx, reporter.ReportInterval)

	for i, question := range questions {
		wg.Add(1)
		go func(index int, q types.Question) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			tracker.Start(index)
			result := benchmarkQuestion(client, model, q, index, benchmarkCfg)
			results[index] = result
			completed := tracker.Finish(index)

			if result.Error == "" {
				correctnessStr := ""
				if result.IsCorrect != nil {
					if *result.IsCorrect {
						correctnessStr = " ✓ Correct"
					} else {
						correctnessStr = " ✗ Wrong"
					}
				}

				finishReasonStr := ""
				if result.FinishReason == "null" || result.FinishReason == "" {
					finishReasonStr = " [⚠ finish_reason=null]"
				} else if result.FinishReason != "stop" {
					finishReasonStr = fmt.Sprintf(" [finish_reason=%s]", result.FinishReason)
				}

				logger.Printf("Question %d completed (%d/%d done): TTFT=%dms, Total=%dms, Tokens=%d, TPS=%.2f%s%s",
					index+1, completed, len(questions), result.TTFT.Milliseconds(),
					result.TotalTime.Milliseconds(), result.TokensUsed, result.TPS, correctnessStr, finishReasonStr)
			} else {
				logger.Printf("Question %d failed (%d/%d done): %s", index+1, completed, len(questions), result.Error)
			}
		}(i, question)
	}

	wg.Wait()
	stopMonitor()
	tracker.Report()
	return results
}

// benchmarkQuestion 对单个问题进行测试
func benchmarkQuestion(client *openai.Client, model string, q types.Question, index int, benchmarkCfg config.BenchmarkConfig) types.BenchmarkResult {
	result := types.BenchmarkResult{
		QuestionIndex:  index,
		Dataset:        q.Dataset,
		Question:       q.Question,
		ExpectedAnswer: q.Answer,
	}

	// 设置超时时间为30分钟
	ctx, cancel := context.WithTimeout(context.Background(), TimeoutPerRequest)
	defer cancel()
	startTime := time.Now()

	// 创建请求
	req := openai.ChatCompletionRequest{
		Model:               model,
		MaxCompletionTokens: benchmarkCfg.MaxTokens,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: q.Question,
			},
		},
		Stream: true,
		// 请求流结束前附加携带 usage 的最终 chunk，用于精确 token 统计
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	}
	if benchmarkCfg.ReasoningEffort != "" {
		req.ReasoningEffort = strings.ToLower(benchmarkCfg.ReasoningEffort)
	}
	if benchmarkCfg.Temperature != nil {
		req.Temperature = *benchmarkCfg.Temperature
	}
	if benchmarkCfg.TopP != nil {
		req.TopP = *benchmarkCfg.TopP
	}
	if len(benchmarkCfg.Thinking) > 0 {
		req.THINKING = benchmarkCfg.Thinking
	}
	result.RawRequest = &req

	// 发送流式请求
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to create stream: %v", err)
		return result
	}
	defer func(stream *openai.ChatCompletionStream) { _ = stream.Close() }(stream)

	var firstTokenTime time.Time
	var fullResponse strings.Builder
	var rawResponses []openai.ChatCompletionStreamResponse
	receivedFirstToken := false
	var finishReason string

	// 读取流式响应
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			result.Error = fmt.Sprintf("Stream error: %v", err)
			return result
		}
		rawResponses = append(rawResponses, response)

		// 记录首个 token 时间
		if !receivedFirstToken && len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			firstTokenTime = time.Now()
			receivedFirstToken = true
			result.TTFT = firstTokenTime.Sub(startTime)
			logger.Printf("Question %d first token received (TTFT=%dms)", index+1, result.TTFT.Milliseconds())
		}

		// 收集完整响应
		if len(response.Choices) > 0 {
			content := response.Choices[0].Delta.Content
			fullResponse.WriteString(content)

			// 捕获 finish_reason
			if response.Choices[0].FinishReason != "" {
				finishReason = string(response.Choices[0].FinishReason)
			}
		}
	}

	// 设置 finish_reason
	if finishReason != "" {
		result.FinishReason = finishReason
	} else {
		result.FinishReason = "null"
	}

	endTime := time.Now()
	result.TotalTime = endTime.Sub(startTime)
	lastUsage, chunkCount := collectUsage(rawResponses)
	// 优先采用 usage 上报的精确 completion token 数；
	// 网关不支持 stream_options.include_usage 时回退到旧行为（chunk 计数）
	if lastUsage != nil {
		result.TokensUsed = lastUsage.CompletionTokens
		result.PromptTokens = lastUsage.PromptTokens
		if lastUsage.PromptTokensDetails != nil {
			result.CachedTokens = lastUsage.PromptTokensDetails.CachedTokens
		}
		if lastUsage.CompletionTokensDetails != nil {
			result.ReasoningTokens = lastUsage.CompletionTokensDetails.ReasoningTokens
		}
	} else {
		result.TokensUsed = chunkCount
	}
	result.ModelAnswer = fullResponse.String()
	result.RawResponse = rawResponses
	result.RawResponseHeader = stream.Header()

	// 计算 TPS 和 TPM
	if result.TotalTime.Seconds() > 0 {
		result.TPS = float64(result.TokensUsed) / result.TotalTime.Seconds()
		result.TPM = result.TPS * 60
	}

	// 提取答案
	result.ExtractedAnswer = extractAnswer(result.ModelAnswer)

	// 验证答案（如果有标准答案）
	if q.Answer != nil {
		isCorrect := compareAnswers(result.ExtractedAnswer, *q.Answer)
		result.IsCorrect = &isCorrect
	}

	return result
}

// extractAnswer 从模型回答中提取 \boxed{} 中的答案。
// 优先在剔除思考内容后的正文中提取；若正文中没有 \boxed{}（例如思考标签异常
// 或回答被截断），再回退到在完整回答中提取。
func extractAnswer(response string) string {
	if answer := extractLastBoxed(stripReasoning(response)); answer != "" {
		return answer
	}
	return extractLastBoxed(response)
}

// reasoningCloseTags 是各家模型思考内容的常见闭合标签。
// 只匹配闭合标签而不做成对匹配，是因为部分模型或网关会丢失起始标签，仅保留闭合标签。
var reasoningCloseTags = []string{
	"</think>",
	"</thinking>",
	"</reasoning>",
	"</thought>",
	"◁/think▷",
	"[/THINK]",
}

// stripReasoning 剔除模型回答中的思考内容，返回最后一个思考闭合标签之后的正文。
// 思考内容里可能出现干扰判题的 \boxed{}（甚至是空的），而最终答案总是在思考结束之后给出。
func stripReasoning(response string) string {
	cut := -1 // 最靠后的闭合标签的结束位置
	for _, tag := range reasoningCloseTags {
		if idx := strings.LastIndex(response, tag); idx != -1 {
			if end := idx + len(tag); end > cut {
				cut = end
			}
		}
	}
	if cut == -1 {
		return response
	}
	return response[cut:]
}

// extractLastBoxed 提取文本中最后一个内容非空的 \boxed{...}，支持嵌套的大括号。
// 取最后一个而不是第一个，是因为模型可能在推导过程中多次提及 \boxed{}，
// 而最终答案在结尾给出。
func extractLastBoxed(text string) string {
	const marker = "\\boxed{"

	for searchEnd := len(text); searchEnd > 0; {
		start := strings.LastIndex(text[:searchEnd], marker)
		if start == -1 {
			return ""
		}
		searchEnd = start // 本次提取失败时，从这里继续向前查找上一个 \boxed{

		start += len(marker)
		braceCount := 1
		end := start

		for end < len(text) && braceCount > 0 {
			if text[end] == '{' {
				braceCount++
			} else if text[end] == '}' {
				braceCount--
			}
			if braceCount > 0 {
				end++
			}
		}

		if braceCount != 0 {
			continue // 大括号未闭合（例如回答被截断），尝试更前面的 \boxed{
		}

		// 清理答案：移除多余的空格和换行
		answer := strings.TrimSpace(text[start:end])
		answer = strings.ReplaceAll(answer, "\n", " ")
		answer = strings.ReplaceAll(answer, "  ", " ")
		if answer != "" {
			return answer
		}
	}

	return ""
}

// compareAnswers 比较提取的答案和标准答案是否一致
func compareAnswers(extracted, expected string) bool {
	// 标准化答案：移除空格、转小写
	normalize := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, " ", "")
		s = strings.ReplaceAll(s, ",", "")
		return s
	}

	extractedNorm := normalize(extracted)
	expectedNorm := normalize(expected)

	return extractedNorm == expectedNorm
}
