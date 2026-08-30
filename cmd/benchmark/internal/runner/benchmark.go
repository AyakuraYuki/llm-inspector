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
	"github.com/AyakuraYuki/llm-inspector/internal/llm/tokstats"
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

				decodeStr := ", DecodeTPS=excluded"
				if result.DecodeValid {
					decodeStr = fmt.Sprintf(", DecodeTPS=%.2f", result.TPSDecode)
				}

				logger.Printf("Question %d completed (%d/%d done): TTFT=%dms, Total=%dms, Tokens=%d, E2ETPS=%.2f%s%s%s",
					index+1, completed, len(questions), result.TTFT.Milliseconds(),
					result.TotalTime.Milliseconds(), result.TokensUsed, result.TPSE2E, decodeStr, correctnessStr, finishReasonStr)
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
	defer stream.Close()

	var fullResponse strings.Builder
	var reasoningResponse strings.Builder
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
			// 出错时也保留响应 Header，便于结合请求错误日志按 RequestID 排查
			result.RawResponseHeader = stream.Header()
			return result
		}
		rawResponses = append(rawResponses, response)

		if len(response.Choices) > 0 {
			delta := response.Choices[0].Delta

			// 记录首个 token 时间：思考内容（reasoning_content）也是模型正在
			// 解码的 token，只看正文 Content 会把 TTFT 推迟到思考结束，
			// 使生成窗口萎缩成正文段、解码 TPS 被虚高数倍
			if !receivedFirstToken && (delta.Content != "" || delta.ReasoningContent != "") {
				receivedFirstToken = true
				result.TTFT = time.Since(startTime)
				logger.Printf("Question %d first token received (TTFT=%dms)", index+1, result.TTFT.Milliseconds())
			}

			// 收集完整响应（思考内容单独收集，供无 usage 时的 token 估算与排查）
			fullResponse.WriteString(delta.Content)
			reasoningResponse.WriteString(delta.ReasoningContent)

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

	result.TotalTime = time.Since(startTime)
	lastUsage := collectUsage(rawResponses)
	// 优先采用 usage 上报的精确 completion token 数；
	// 网关不支持 stream_options.include_usage 时按文本构成估算
	// （正文 + 思考内容），并打估算标记
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
		result.TokensUsed = int(tokstats.EstimateTokens(fullResponse.String() + reasoningResponse.String()))
		result.TokensEstimated = true
	}
	result.ModelAnswer = fullResponse.String()
	result.RawResponse = rawResponses
	result.RawResponseHeader = stream.Header()

	// 端到端 TPS/TPM：用户感知速度，分母含 TTFT，推理模型会被思考时间摊薄
	if result.TotalTime.Seconds() > 0 {
		result.TPSE2E = float64(result.TokensUsed) / result.TotalTime.Seconds()
		result.TPME2E = result.TPSE2E * 60
	}

	// 解码 TPS/TPM：分母为生成窗口（E2E − TTFT），须经有效性校验——
	// 生成窗口双门槛剔除「响应缓冲后一次性到达」的排空样本，
	// 单流物理天花板兜住任何漏网的虚高形态；未捕获到 TTFT 时窗口无定义，直接判伪
	genWindow := result.TotalTime - result.TTFT
	if result.TTFT > 0 && tokstats.ValidStreamTPS(int64(result.TokensUsed), genWindow, result.TotalTime) {
		result.TPSDecode = float64(result.TokensUsed) / genWindow.Seconds()
		result.TPMDecode = result.TPSDecode * 60
		result.DecodeValid = true
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
