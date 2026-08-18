package report

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

// OutputResults 将结果输出到报告目录下的 JSON 文件
func OutputResults(results []types.BenchmarkResult, reportDir string) {
	serializableResults := make([]types.SerializableResult, len(results))
	for i, r := range results {
		serializableResults[i] = types.SerializableResult{
			Dataset:         r.Dataset,
			QuestionIndex:   r.QuestionIndex,
			Question:        r.Question,
			ExpectedAnswer:  r.ExpectedAnswer,
			ModelAnswer:     r.ModelAnswer,
			ExtractedAnswer: r.ExtractedAnswer,
			IsCorrect:       r.IsCorrect,
			FinishReason:    r.FinishReason,
			TTFTMs:          r.TTFT.Milliseconds(),
			TotalTimeMs:     r.TotalTime.Milliseconds(),
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

	logger.Printf("Results saved to: %s", filename)
}

// SaveIndividualReports 为每个问题保存单独的详细报告
func SaveIndividualReports(results []types.BenchmarkResult, reportDir string) {
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
			report.WriteString("\n\n")
		}

		// Raw Request
		if r.RawRequest != nil {
			report.WriteString("Raw Request:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			bs, _ := json.MarshalIndent(r.RawRequest, "", "    ")
			report.WriteString(string(bs))
			report.WriteString("\n\n")
		}

		// Raw Response Header
		if len(r.RawResponseHeader) > 0 {
			report.WriteString("Raw Response Header:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			for name, values := range r.RawResponseHeader {
				report.WriteString(fmt.Sprintf("%s: %v\n", name, values))
			}
			report.WriteString("\n")
		}

		// Raw Response
		if r.RawResponse != nil {
			report.WriteString("Raw Response:\n")
			report.WriteString(strings.Repeat("-", 80) + "\n")
			for _, response := range r.RawResponse {
				bs, _ := json.Marshal(response)
				report.WriteString(string(bs) + "\n")
			}
			report.WriteString("\n")
		}

		report.WriteString("\n")
		report.WriteString(strings.Repeat("=", 80) + "\n")

		// 写入文件
		if err := os.WriteFile(filename, []byte(report.String()), 0644); err != nil {
			log.Printf("Failed to write report for question %d: %v", r.QuestionIndex+1, err)
			failCount++
		} else {
			successCount++
		}
	}

	logger.Printf("Individual reports saved to: %s/ (%d success, %d failed)", reportDir, successCount, failCount)
}
