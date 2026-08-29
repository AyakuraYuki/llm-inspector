package reporter

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

// ReportInterval 是心跳监控器输出当前进度的间隔
const ReportInterval = 30 * time.Second

// PrintStatistics 打印统计信息
func PrintStatistics(results []types.BenchmarkResult) {
	var (
		totalTTFT, totalTime       time.Duration
		totalTokens                int64
		totalPromptTokens          int64
		totalCacheTokens           int64
		totalTPS, totalTPM         float64
		successCount               = 0
		correctCount               = 0
		questionsWithAnswer        = 0
		finishReasonCounts         = make(map[string]int) // finish_reason 统计
		datasetQuestionsWithAnswer = make(map[string]int) // 数据集包含答案的问题数量
		datasetCorrectCount        = make(map[string]int) // 数据集答对数量
	)

	for _, r := range results {
		if r.Error == "" {
			totalTTFT += r.TTFT
			totalTime += r.TotalTime
			totalPromptTokens += int64(r.PromptTokens)
			totalTokens += int64(r.TokensUsed + r.PromptTokens) // input + output
			totalCacheTokens += int64(r.CachedTokens)
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
				datasetQuestionsWithAnswer[r.Dataset]++
				if r.IsCorrect != nil && *r.IsCorrect {
					correctCount++
					datasetCorrectCount[r.Dataset]++
				}
			}
		}
	}

	if successCount == 0 {
		fmt.Println("\nNo successful benchmarks to report statistics")
		return
	}

	logger.Printf("")
	logger.Printf("%s", strings.Repeat("=", 60))
	logger.Printf("BENCHMARK SUMMARY")
	logger.Printf("%s", strings.Repeat("=", 60))
	logger.Printf("Total questions: %d", len(results))
	logger.Printf("Successful: %d", successCount)
	logger.Printf("Failed: %d", len(results)-successCount)
	logger.Printf("")

	// finish_reason 分布
	logger.Printf("Finish Reason Distribution:")
	for reason, count := range finishReasonCounts {
		percentage := float64(count) / float64(successCount) * 100
		logger.Printf("  %s: %d (%.1f%%)", reason, count, percentage)
	}
	logger.Printf("")

	// 答案正确性统计
	if questionsWithAnswer > 0 {
		accuracy := float64(correctCount) / float64(questionsWithAnswer) * 100
		logger.Printf("Questions with answers: %d", questionsWithAnswer)
		logger.Printf("Correct answers: %d", correctCount)
		logger.Printf("Wrong answers: %d", questionsWithAnswer-correctCount)
		logger.Printf("Accuracy: %.2f%%", accuracy)
		logger.Printf("In datasets:")
		for _, dataset := range slices.Sorted(maps.Keys(datasetQuestionsWithAnswer)) {
			datasetAccuracy := float64(datasetCorrectCount[dataset]) / float64(datasetQuestionsWithAnswer[dataset]) * 100
			logger.Printf("  - %s: %d/%d (%.2f%%)", dataset, datasetCorrectCount[dataset], datasetQuestionsWithAnswer[dataset], datasetAccuracy)
		}
		logger.Printf("")
	}

	// 性能统计
	logger.Printf("Average TTFT: %d ms", totalTTFT.Milliseconds()/int64(successCount))
	logger.Printf("Average Total Time: %d ms", totalTime.Milliseconds()/int64(successCount))
	logger.Printf("Average Tokens: %d", totalTokens/int64(successCount))
	logger.Printf("Average TPS: %.2f", totalTPS/float64(successCount))
	logger.Printf("Average TPM: %.2f", totalTPM/float64(successCount))
	if totalPromptTokens > 0 {
		logger.Printf("Cache Hit Ratio: %.2f%%", float64(totalCacheTokens)/float64(totalPromptTokens)*100)
	} else {
		logger.Printf("Cache Hit Ratio: n/a")
	}
	logger.Printf("%s", strings.Repeat("=", 60))
}
