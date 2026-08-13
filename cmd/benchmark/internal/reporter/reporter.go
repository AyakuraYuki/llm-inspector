package reporter

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
)

// ReportInterval 是心跳监控器输出当前进度的间隔
const ReportInterval = 30 * time.Second

// PrintStatistics 打印统计信息
func PrintStatistics(results []types.BenchmarkResult) {
	var (
		totalTTFT, totalTime       time.Duration
		totalTokens                int
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
		fmt.Printf("In datasets:\n")
		for _, dataset := range slices.Sorted(maps.Keys(datasetQuestionsWithAnswer)) {
			datasetAccuracy := float64(datasetCorrectCount[dataset]) / float64(datasetQuestionsWithAnswer[dataset]) * 100
			fmt.Printf("  - %s: %d/%d (%.2f%%)\n", dataset, datasetCorrectCount[dataset], datasetQuestionsWithAnswer[dataset], datasetAccuracy)
		}
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
