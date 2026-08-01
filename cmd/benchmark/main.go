package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
)

// BenchmarkConfig 包含 benchmark 运行配置
type BenchmarkConfig struct {
	MaxTokens     int    `json:"max_tokens"`
	MaxWorkers    int    `json:"max_workers"`
	ThinkingStyle string `json:"thinking_style"`
}

// Question 表示一个问题及其答案
type Question struct {
	Question string  `json:"question"`
	Answer   *string `json:"answer"` // 可能为 null
}

// BenchmarkResult 包含单个问题的测试结果
type BenchmarkResult struct {
	QuestionIndex   int           `json:"question_index"`
	Question        string        `json:"question"`
	ExpectedAnswer  *string       `json:"expected_answer,omitempty"` // 标准答案
	ModelAnswer     string        `json:"model_answer"`              // 模型的完整回答
	ExtractedAnswer string        `json:"extracted_answer"`          // 从回答中提取的答案
	IsCorrect       *bool         `json:"is_correct,omitempty"`      // 答案是否正确（如果有标准答案）
	FinishReason    string        `json:"finish_reason,omitempty"`   // 完成原因：stop, length, null 等
	TTFT            time.Duration `json:"ttft_ms"`                   // Time To First Token (ms)
	TotalTime       time.Duration `json:"total_time_ms"`             // 总用时 (ms)
	TokensUsed      int           `json:"tokens_used"`               // 生成的 token 数
	TPS             float64       `json:"tps"`                       // Tokens Per Second
	TPM             float64       `json:"tpm"`                       // Tokens Per Minute
	Error           string        `json:"error,omitempty"`           // 错误信息
}

func main() {
	// 读取配置
	config := BenchmarkConfig{
		MaxTokens:     65536,
		MaxWorkers:    1,
		ThinkingStyle: "enabled", // 启用思考模式
	}

	// 从环境变量读取 API 配置
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "gpt-4"
	}

	// 读取问题列表
	questions, err := loadQuestions("questions.json")
	if err != nil {
		log.Fatalf("Failed to load questions: %v", err)
	}

	fmt.Printf("Loaded %d questions\n", len(questions))
	fmt.Printf("Config: max_tokens=%d, max_workers=%d, thinking_style=%s\n",
		config.MaxTokens, config.MaxWorkers, config.ThinkingStyle)
	fmt.Printf("Model: %s, Base URL: %s\n\n", modelName, baseURL)

	// 创建 OpenAI 客户端
	clientConfig := openai.DefaultConfig(apiKey)
	clientConfig.BaseURL = baseURL
	client := openai.NewClientWithConfig(clientConfig)

	// 运行 benchmark
	results := runBenchmark(client, modelName, questions, config)

	// 输出结果
	outputResults(results)

	// 保存每个问题的详细报告
	saveIndividualReports(results)

	// 计算统计信息
	printStatistics(results)
}

// loadQuestions 从 JSON 文件加载问题
func loadQuestions(filename string) ([]Question, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var questions []Question
	if err := json.Unmarshal(data, &questions); err != nil {
		return nil, err
	}

	return questions, nil
}

// runBenchmark 执行 benchmark 测试
func runBenchmark(client *openai.Client, model string, questions []Question, config BenchmarkConfig) []BenchmarkResult {
	results := make([]BenchmarkResult, len(questions))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, config.MaxWorkers)

	for i, question := range questions {
		wg.Add(1)
		go func(index int, q Question) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			fmt.Printf("[%d/%d] Processing question %d...\n", index+1, len(questions), index+1)
			result := benchmarkQuestion(client, model, q, index, config)
			results[index] = result

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

				fmt.Printf("[%d/%d] Completed: TTFT=%dms, Total=%dms, Tokens=%d, TPS=%.2f%s%s\n",
					index+1, len(questions), result.TTFT.Milliseconds(),
					result.TotalTime.Milliseconds(), result.TokensUsed, result.TPS, correctnessStr, finishReasonStr)
			} else {
				fmt.Printf("[%d/%d] Error: %s\n", index+1, len(questions), result.Error)
			}
		}(i, question)
	}

	wg.Wait()
	return results
}

// benchmarkQuestion 对单个问题进行测试
func benchmarkQuestion(client *openai.Client, model string, q Question, index int, config BenchmarkConfig) BenchmarkResult {
	result := BenchmarkResult{
		QuestionIndex:  index,
		Question:       q.Question,
		ExpectedAnswer: q.Answer,
	}

	// 设置超时时间为30分钟
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	startTime := time.Now()

	// 创建请求
	req := openai.ChatCompletionRequest{
		Model:     model,
		MaxTokens: config.MaxTokens,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: q.Question,
			},
		},
		Stream: true,
	}

	// 发送流式请求
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to create stream: %v", err)
		return result
	}
	defer stream.Close()

	var firstTokenTime time.Time
	var totalTokens int
	var fullResponse strings.Builder
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

		// 记录首个 token 时间
		if !receivedFirstToken && len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
			firstTokenTime = time.Now()
			receivedFirstToken = true
			result.TTFT = firstTokenTime.Sub(startTime)
		}

		// 收集完整响应
		if len(response.Choices) > 0 {
			content := response.Choices[0].Delta.Content
			fullResponse.WriteString(content)
			totalTokens++

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
	result.TokensUsed = totalTokens
	result.ModelAnswer = fullResponse.String()

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

// outputResults 将结果输出到 JSON 文件
func outputResults(results []BenchmarkResult) {
	// 转换为可序列化的格式
	type SerializableResult struct {
		QuestionIndex   int     `json:"question_index"`
		Question        string  `json:"question"`
		ExpectedAnswer  *string `json:"expected_answer,omitempty"`
		ModelAnswer     string  `json:"model_answer"`
		ExtractedAnswer string  `json:"extracted_answer"`
		IsCorrect       *bool   `json:"is_correct,omitempty"`
		FinishReason    string  `json:"finish_reason,omitempty"`
		TTFT_MS         int64   `json:"ttft_ms"`
		TotalTime_MS    int64   `json:"total_time_ms"`
		TokensUsed      int     `json:"tokens_used"`
		TPS             float64 `json:"tps"`
		TPM             float64 `json:"tpm"`
		Error           string  `json:"error,omitempty"`
	}

	serializableResults := make([]SerializableResult, len(results))
	for i, r := range results {
		serializableResults[i] = SerializableResult{
			QuestionIndex:   r.QuestionIndex,
			Question:        r.Question,
			ExpectedAnswer:  r.ExpectedAnswer,
			ModelAnswer:     r.ModelAnswer,
			ExtractedAnswer: r.ExtractedAnswer,
			IsCorrect:       r.IsCorrect,
			FinishReason:    r.FinishReason,
			TTFT_MS:         r.TTFT.Milliseconds(),
			TotalTime_MS:    r.TotalTime.Milliseconds(),
			TokensUsed:      r.TokensUsed,
			TPS:             r.TPS,
			TPM:             r.TPM,
			Error:           r.Error,
		}
	}

	// 生成输出文件名
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("benchmark_results_%s.json", timestamp)

	data, err := json.MarshalIndent(serializableResults, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("Failed to write results: %v", err)
		return
	}

	fmt.Printf("\nResults saved to: %s\n", filename)
}

// saveIndividualReports 为每个问题保存单独的详细报告
func saveIndividualReports(results []BenchmarkResult) {
	// 创建报告目录
	timestamp := time.Now().Format("20060102_150405")
	reportDir := fmt.Sprintf("reports_%s", timestamp)

	if err := os.MkdirAll(reportDir, 0755); err != nil {
		log.Printf("Failed to create report directory: %v", err)
		return
	}

	successCount := 0
	failCount := 0

	for _, r := range results {
		// 生成报告文件名
		filename := fmt.Sprintf("%s/question_%03d.txt", reportDir, r.QuestionIndex+1)

		// 构建报告内容
		var report strings.Builder
		report.WriteString("=" + strings.Repeat("=", 79) + "\n")
		report.WriteString(fmt.Sprintf("QUESTION #%d BENCHMARK REPORT\n", r.QuestionIndex+1))
		report.WriteString("=" + strings.Repeat("=", 79) + "\n\n")

		// 问题部分
		report.WriteString("QUESTION:\n")
		report.WriteString(strings.Repeat("-", 80) + "\n")
		report.WriteString(r.Question + "\n\n")

		// 标准答案（如果有）
		if r.ExpectedAnswer != nil {
			report.WriteString("EXPECTED ANSWER:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			report.WriteString(*r.ExpectedAnswer + "\n\n")
		}

		// 模型响应部分
		report.WriteString("MODEL RESPONSE:\n")
		report.WriteString(strings.Repeat("-", 80) + "\n")
		if r.Error != "" {
			report.WriteString(fmt.Sprintf("ERROR: %s\n\n", r.Error))

			// 添加错误详情分析
			report.WriteString("ERROR ANALYSIS:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			if strings.Contains(r.Error, "EOF") {
				report.WriteString("• Connection terminated unexpectedly (EOF)\n")
				report.WriteString("• Possible causes: Network instability, API server issue, or timeout\n")
			} else if strings.Contains(r.Error, "unexpected end of JSON input") {
				report.WriteString("• Incomplete JSON response received\n")
				report.WriteString("• Possible causes: Stream interrupted, API error, or network issue\n")
			} else if strings.Contains(r.Error, "timeout") || strings.Contains(r.Error, "context deadline exceeded") {
				report.WriteString("• Request exceeded timeout limit (30 minutes)\n")
				report.WriteString("• The model took too long to respond\n")
			} else if strings.Contains(r.Error, "Failed to create stream") {
				report.WriteString("• Failed to establish streaming connection\n")
				report.WriteString("• Possible causes: API endpoint unreachable, authentication issue, or rate limit\n")
			} else {
				report.WriteString("• Unexpected error occurred\n")
			}
			report.WriteString("\n")
		} else {
			report.WriteString(r.ModelAnswer + "\n\n")

			// 如果finish_reason异常，添加警告说明
			if r.FinishReason == "null" || r.FinishReason == "" {
				report.WriteString("⚠ WARNING: Response finished abnormally\n")
				report.WriteString("ABNORMAL FINISH DETAILS:\n")
				report.WriteString(strings.Repeat("-", 80) + "\n")
				report.WriteString("• finish_reason is null or empty\n")
				report.WriteString("• The model response was interrupted or terminated unexpectedly\n")
				report.WriteString("• This may result in incomplete or missing answers\n")
				report.WriteString("• Possible causes: Token limit reached, connection lost, or API issue\n\n")
			} else if r.FinishReason != "stop" {
				report.WriteString(fmt.Sprintf("⚠ WARNING: Non-normal finish reason: %s\n", r.FinishReason))
				report.WriteString("FINISH REASON DETAILS:\n")
				report.WriteString(strings.Repeat("-", 80) + "\n")
				if r.FinishReason == "length" {
					report.WriteString("• Response stopped due to max_tokens limit\n")
					report.WriteString("• The answer may be incomplete\n")
				} else {
					report.WriteString(fmt.Sprintf("• Unexpected finish reason: %s\n", r.FinishReason))
				}
				report.WriteString("\n")
			}
		}

		// 提取的答案
		if r.ExtractedAnswer != "" {
			report.WriteString("EXTRACTED ANSWER:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			report.WriteString(r.ExtractedAnswer + "\n\n")
		}

		// 验证结果
		if r.IsCorrect != nil {
			report.WriteString("VERIFICATION:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			if *r.IsCorrect {
				report.WriteString("✓ CORRECT\n\n")
			} else {
				report.WriteString("✗ WRONG\n")
				report.WriteString(fmt.Sprintf("  Expected: %s\n", *r.ExpectedAnswer))
				report.WriteString(fmt.Sprintf("  Got:      %s\n\n", r.ExtractedAnswer))
			}
		}

		// 性能指标
		report.WriteString("PERFORMANCE METRICS:\n")
		report.WriteString(strings.Repeat("-", 80) + "\n")
		report.WriteString(fmt.Sprintf("TTFT (Time To First Token): %d ms\n", r.TTFT.Milliseconds()))
		report.WriteString(fmt.Sprintf("Total Time:                 %d ms\n", r.TotalTime.Milliseconds()))
		report.WriteString(fmt.Sprintf("Tokens Generated:           %d\n", r.TokensUsed))
		report.WriteString(fmt.Sprintf("TPS (Tokens Per Second):    %.2f\n", r.TPS))
		report.WriteString(fmt.Sprintf("TPM (Tokens Per Minute):    %.2f\n", r.TPM))

		// Finish Reason
		if r.FinishReason != "" {
			report.WriteString(fmt.Sprintf("Finish Reason:              %s", r.FinishReason))
			if r.FinishReason == "null" || r.FinishReason == "" {
				report.WriteString(" ⚠ WARNING: Abnormal termination")
			} else if r.FinishReason != "stop" {
				report.WriteString(fmt.Sprintf(" ⚠ WARNING: Non-normal finish"))
			}
			report.WriteString("\n")
		}

		report.WriteString("\n")
		report.WriteString("=" + strings.Repeat("=", 79) + "\n")

		// 写入文件
		if err := os.WriteFile(filename, []byte(report.String()), 0644); err != nil {
			log.Printf("Failed to write report for question %d: %v", r.QuestionIndex+1, err)
			failCount++
		} else {
			successCount++
		}
	}

	fmt.Printf("Individual reports saved to: %s/ (%d success, %d failed)\n", reportDir, successCount, failCount)
}

// extractAnswer 从模型回答中提取 \boxed{} 中的答案
func extractAnswer(response string) string {
	// 查找 \boxed{...} 模式
	// 支持嵌套的大括号
	start := strings.Index(response, "\\boxed{")
	if start == -1 {
		return ""
	}

	start += len("\\boxed{")
	braceCount := 1
	end := start

	for end < len(response) && braceCount > 0 {
		if response[end] == '{' {
			braceCount++
		} else if response[end] == '}' {
			braceCount--
		}
		if braceCount > 0 {
			end++
		}
	}

	if braceCount == 0 {
		answer := response[start:end]
		// 清理答案：移除多余的空格和换行
		answer = strings.TrimSpace(answer)
		answer = strings.ReplaceAll(answer, "\n", " ")
		answer = strings.ReplaceAll(answer, "  ", " ")
		return answer
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

// printStatistics 打印统计信息
func printStatistics(results []BenchmarkResult) {
	var totalTTFT, totalTime time.Duration
	var totalTokens int
	var totalTPS, totalTPM float64
	successCount := 0
	correctCount := 0
	questionsWithAnswer := 0

	// finish_reason 统计
	finishReasonCounts := make(map[string]int)

	for _, r := range results {
		if r.Error == "" {
			totalTTFT += r.TTFT
			totalTime += r.TotalTime
			totalTokens += r.TokensUsed
			totalTPS += r.TPS
			totalTPM += r.TPM
			successCount++

			// 统计 finish_reason
			if r.FinishReason != "" {
				finishReasonCounts[r.FinishReason]++
			}

			// 统计答案正确性
			if r.ExpectedAnswer != nil {
				questionsWithAnswer++
				if r.IsCorrect != nil && *r.IsCorrect {
					correctCount++
				}
			}
		}
	}

	if successCount == 0 {
		fmt.Println("\nNo successful benchmarks to report statistics")
		return
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("BENCHMARK STATISTICS")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total questions: %d\n", len(results))
	fmt.Printf("Successful: %d\n", successCount)
	fmt.Printf("Failed: %d\n", len(results)-successCount)
	fmt.Println()

	// finish_reason 分布
	fmt.Println("Finish Reason Distribution:")
	for reason, count := range finishReasonCounts {
		percentage := float64(count) / float64(successCount) * 100
		fmt.Printf("  %s: %d (%.1f%%)\n", reason, count, percentage)
	}
	fmt.Println()

	// 答案正确性统计
	if questionsWithAnswer > 0 {
		accuracy := float64(correctCount) / float64(questionsWithAnswer) * 100
		fmt.Printf("Questions with answers: %d\n", questionsWithAnswer)
		fmt.Printf("Correct answers: %d\n", correctCount)
		fmt.Printf("Wrong answers: %d\n", questionsWithAnswer-correctCount)
		fmt.Printf("Accuracy: %.2f%%\n", accuracy)
		fmt.Println()
	}

	// 性能统计
	fmt.Printf("Average TTFT: %d ms\n", totalTTFT.Milliseconds()/int64(successCount))
	fmt.Printf("Average Total Time: %d ms\n", totalTime.Milliseconds()/int64(successCount))
	fmt.Printf("Average Tokens: %d\n", totalTokens/successCount)
	fmt.Printf("Average TPS: %.2f\n", totalTPS/float64(successCount))
	fmt.Printf("Average TPM: %.2f\n", totalTPM/float64(successCount))
	fmt.Println(strings.Repeat("=", 60))
}
