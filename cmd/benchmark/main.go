package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert/yaml"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/util"
)

// Config 从 YAML 加载运行所需的配置
type Config struct {
	BaseURL         string     `yaml:"base_url"`
	APIKey          string     `yaml:"api_key"`
	Model           string     `yaml:"model"`
	MaxTokens       int        `yaml:"max_tokens"`
	MaxWorkers      int        `yaml:"max_workers"`
	ReasoningEffort string     `yaml:"reasoning_effort"`
	HFDataset       []string   `yaml:"hf_dataset"`
	CustomQuestions []Question `yaml:"custom_questions"`
}

func (cfg *Config) validate() error {
	if cfg.BaseURL == "" {
		return errors.New("缺少 base_url")
	}
	if cfg.APIKey == "" {
		return errors.New("缺少 api_key")
	}
	if cfg.Model == "" {
		return errors.New("缺少 model")
	}
	if len(cfg.HFDataset) == 0 && len(cfg.CustomQuestions) == 0 {
		return errors.New("缺少测试数据集")
	}
	return nil
}

// BenchmarkConfig 包含 benchmark 运行配置
type BenchmarkConfig struct {
	MaxTokens       int    `json:"max_tokens"`
	MaxWorkers      int    `json:"max_workers"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func (cfg *Config) BenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		MaxTokens:       util.Ternary(cfg.MaxTokens > 0, cfg.MaxTokens, 65536),
		MaxWorkers:      max(cfg.MaxWorkers, 1),
		ReasoningEffort: cfg.ReasoningEffort,
	}
}

// progressReportInterval 是心跳监控器输出当前进度的间隔
const progressReportInterval = 30 * time.Second

// Question 表示一个问题及其答案
type Question struct {
	Dataset  string  `json:"dataset" yaml:"dataset"`
	Question string  `json:"question" yaml:"question"`
	Answer   *string `json:"answer" yaml:"answer"` // 可能为 null
}

// BenchmarkResult 包含单个问题的测试结果
type BenchmarkResult struct {
	QuestionIndex   int           `json:"question_index"`
	Dataset         string        `json:"dataset"`
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
	configPath := flag.String("config", "", "启动配置 YAML（必填）")
	flag.Parse()
	if configPath == nil || *configPath == "" {
		_, _ = fmt.Fprintln(os.Stderr, "错误: 缺少 -config")
		os.Exit(2)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}

	config := cfg.BenchmarkConfig()

	// 读取问题列表
	var questions []Question
	for _, dataset := range cfg.HFDataset {
		problems, _ := loadAIMEProblemsFromHFDataset(dataset)
		questions = append(questions, problems...)
	}
	for _, question := range cfg.CustomQuestions {
		// 自定义问题清单标记数据集名称
		question.Dataset = "__custom_questions__"
		questions = append(questions, question)
	}

	logf("Loaded %d questions", len(questions))
	logf("Config: max_tokens=%d, max_workers=%d", config.MaxTokens, config.MaxWorkers)
	logf("Model: %s, Base URL: %s", cfg.Model, cfg.BaseURL)

	// 创建 OpenAI 客户端
	clientConfig := openai.DefaultConfig(cfg.APIKey)
	clientConfig.BaseURL = cfg.BaseURL
	client := openai.NewClientWithConfig(clientConfig)

	// 运行 benchmark
	logf("Benchmark started")
	results := runBenchmark(client, cfg.Model, questions, config)
	logf("Benchmark finished")

	// 创建统一的报告目录，本次运行的所有输出都存放在此
	reportDir := fmt.Sprintf("reports_%s", time.Now().Format("20060102_150405"))
	if err = os.MkdirAll(reportDir, os.ModePerm); err != nil {
		log.Fatalf("Failed to create report directory: %v", err)
	}

	// 输出 JSON 结果
	outputResults(results, reportDir)

	// 保存每个问题的详细报告
	saveIndividualReports(results, reportDir)

	// 计算统计信息
	printStatistics(results)
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadAIMEProblemsFromHFDataset(dataset string) ([]Question, error) {
	data, err := os.ReadFile(dataset)
	if err != nil {
		return nil, err
	}

	var result struct {
		Rows []struct {
			Row struct {
				Problem string `json:"problem"`
				Answer  string `json:"answer"`
			} `json:"row"`
		} `json:"rows"`
	}

	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	var questions []Question
	for _, r := range result.Rows {
		questions = append(questions, Question{
			Dataset:  dataset,
			Question: fmt.Sprintf("%s\n\nPlease reason step by step, and put your final answer within \\boxed{}.", r.Row.Problem),
			Answer:   new(r.Row.Answer),
		})
	}

	return questions, nil
}

// runBenchmark 执行 benchmark 测试
func runBenchmark(client *openai.Client, model string, questions []Question, config BenchmarkConfig) []BenchmarkResult {
	results := make([]BenchmarkResult, len(questions))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, config.MaxWorkers)

	// 启动心跳监控器，定期输出整体进度和正在执行的测试项目
	tracker := newProgressTracker(len(questions))
	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	go tracker.monitor(monitorCtx, progressReportInterval)

	for i, question := range questions {
		wg.Add(1)
		go func(index int, q Question) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			tracker.start(index)
			result := benchmarkQuestion(client, model, q, index, config)
			results[index] = result
			completed := tracker.finish(index)

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

				logf("Question %d completed (%d/%d done): TTFT=%dms, Total=%dms, Tokens=%d, TPS=%.2f%s%s",
					index+1, completed, len(questions), result.TTFT.Milliseconds(),
					result.TotalTime.Milliseconds(), result.TokensUsed, result.TPS, correctnessStr, finishReasonStr)
			} else {
				logf("Question %d failed (%d/%d done): %s", index+1, completed, len(questions), result.Error)
			}
		}(i, question)
	}

	wg.Wait()
	stopMonitor()
	tracker.report()
	return results
}

// benchmarkQuestion 对单个问题进行测试
func benchmarkQuestion(client *openai.Client, model string, q Question, index int, config BenchmarkConfig) BenchmarkResult {
	result := BenchmarkResult{
		QuestionIndex:  index,
		Dataset:        q.Dataset,
		Question:       q.Question,
		ExpectedAnswer: q.Answer,
	}

	// 设置超时时间为30分钟
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	startTime := time.Now()

	// 创建请求
	req := openai.ChatCompletionRequest{
		Model:               model,
		MaxCompletionTokens: config.MaxTokens,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: q.Question,
			},
		},
		Stream: true,
	}
	if config.ReasoningEffort != "" {
		req.ReasoningEffort = strings.ToLower(config.ReasoningEffort)
	}

	// 发送流式请求
	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to create stream: %v", err)
		return result
	}
	defer func(stream *openai.ChatCompletionStream) { _ = stream.Close() }(stream)

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
			logf("Question %d first token received (TTFT=%dms)", index+1, result.TTFT.Milliseconds())
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

// outputResults 将结果输出到报告目录下的 JSON 文件
func outputResults(results []BenchmarkResult, reportDir string) {
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

	// 生成输出文件名（存放在报告目录下）
	filename := fmt.Sprintf("%s/benchmark_results.json", reportDir)

	data, err := json.MarshalIndent(serializableResults, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("Failed to write results: %v", err)
		return
	}

	logf("Results saved to: %s", filename)
}

// saveIndividualReports 为每个问题保存单独的详细报告
func saveIndividualReports(results []BenchmarkResult, reportDir string) {
	successCount := 0
	failCount := 0

	for _, r := range results {
		// 生成报告文件名
		filename := fmt.Sprintf("%s/question_%03d_%s.txt", reportDir, r.QuestionIndex+1, r.Dataset)

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
				report.WriteString(" ⚠ WARNING: Non-normal finish")
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

	logf("Individual reports saved to: %s/ (%d success, %d failed)", reportDir, successCount, failCount)
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

// extractAnswer 从模型回答中提取 \boxed{} 中的答案。
// 优先在剔除思考内容后的正文中提取；若正文中没有 \boxed{}（例如思考标签异常
// 或回答被截断），再回退到在完整回答中提取。
func extractAnswer(response string) string {
	if answer := extractLastBoxed(stripReasoning(response)); answer != "" {
		return answer
	}
	return extractLastBoxed(response)
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

// logf 输出带时间戳的日志，用于跟踪测试进度
func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// progressTracker 跟踪 benchmark 的执行进度，用于监控当前正在执行的测试项目
type progressTracker struct {
	total      int
	startTime  time.Time
	completed  int64             // 原子计数：已完成的问题数
	mu         sync.Mutex        // 保护 inProgress
	inProgress map[int]time.Time // 问题索引（0-based） -> 开始执行的时间
}

func newProgressTracker(total int) *progressTracker {
	return &progressTracker{
		total:      total,
		startTime:  time.Now(),
		inProgress: make(map[int]time.Time),
	}
}

// start 标记某个问题开始执行
func (p *progressTracker) start(index int) {
	p.mu.Lock()
	p.inProgress[index] = time.Now()
	p.mu.Unlock()
	logf("Question %d started", index+1)
}

// finish 标记某个问题执行完成，返回累计已完成的问题数
func (p *progressTracker) finish(index int) int {
	p.mu.Lock()
	delete(p.inProgress, index)
	p.mu.Unlock()
	return int(atomic.AddInt64(&p.completed, 1))
}

// report 输出整体进度以及当前正在执行的测试项目及其已运行时长
func (p *progressTracker) report() {
	completed := atomic.LoadInt64(&p.completed)

	p.mu.Lock()
	running := make([]int, 0, len(p.inProgress))
	starts := make(map[int]time.Time, len(p.inProgress))
	for idx, t := range p.inProgress {
		running = append(running, idx)
		starts[idx] = t
	}
	p.mu.Unlock()
	sort.Ints(running)

	elapsed := time.Since(p.startTime).Round(time.Second)
	logf("Progress: %d/%d completed, %d in progress (elapsed %s)", completed, p.total, len(running), elapsed)
	for _, idx := range running {
		logf("  -> question %d running for %s", idx+1, time.Since(starts[idx]).Round(time.Second))
	}
}

// monitor 定期输出进度，直到 ctx 被取消
func (p *progressTracker) monitor(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.report()
		}
	}
}
