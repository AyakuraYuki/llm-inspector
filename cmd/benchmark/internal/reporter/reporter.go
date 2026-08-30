package reporter

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// ReportInterval 是心跳监控器输出当前进度的间隔
const ReportInterval = 30 * time.Second

// PrintStatistics 打印统计信息。elapsed 是整批运行（全部 MaxWorkers 并发）的
// 墙钟耗时，用于计算 System TPS/TPM。
func PrintStatistics(results []types.BenchmarkResult, elapsed time.Duration) {
	var (
		totalTTFT, totalTime       time.Duration
		totalTokens                int64
		totalOutputTokens          int64 // 成功问题的 output tokens 总和，用于 System TPS
		totalPromptTokens          int64
		totalCacheTokens           int64
		totalTPSE2E, totalTPME2E   float64
		totalTPSDecode             float64 // 有效解码样本的 TPS 之和（求均值）
		decodeTokens               int64   // 有效解码样本的 token 总和（求总量比率）
		decodeWindow               time.Duration
		decodeValidCount           int
		tokensEstimatedCount       int
		reasoningMergedCount       int
		successCount               int
		correctCount               int
		questionsWithAnswer        int
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
			totalOutputTokens += int64(r.TokensUsed)
			totalCacheTokens += int64(r.CachedTokens)
			totalTPSE2E += r.TPSE2E
			totalTPME2E += r.TPME2E
			if r.TokensEstimated {
				tokensEstimatedCount++
			}
			if r.ReasoningTokensMerged {
				reasoningMergedCount++
			}
			// 解码速率只统计通过有效性校验的样本：判伪样本（一次性到达的
			// 排空速度、超物理上限的虚高值）若进入平均会拉爆整体
			if r.DecodeValid {
				decodeValidCount++
				totalTPSDecode += r.TPSDecode
				decodeTokens += int64(r.TokensUsed)
				decodeWindow += r.TotalTime - r.TTFT
			}
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

	const (
		labelWidth = 24 // 最长的固定标签「Reasoning tokens merged」为 23 个字符
		ruleWidth  = 60
	)
	rule := strings.Repeat("=", ruleWidth)
	subrule := strings.Repeat("-", ruleWidth-2)

	logger.Printf("")
	logger.Printf("%s", rule)
	logger.Printf("  BENCHMARK SUMMARY")
	logger.Printf("%s", rule)
	logger.Printf("  %-*s: %d", labelWidth, "Total questions", len(results))
	logger.Printf("  %-*s: %d", labelWidth, "Successful", successCount)
	logger.Printf("  %-*s: %d", labelWidth, "Failed", len(results)-successCount)
	logger.Printf("")

	// finish_reason 分布
	logger.Printf("  Finish Reason Distribution")
	logger.Printf("  %s", subrule)
	reasons := slices.Sorted(maps.Keys(finishReasonCounts))
	reasonWidth := util.MaxLen(reasons)
	for _, reason := range reasons {
		count := finishReasonCounts[reason]
		percentage := float64(count) / float64(successCount) * 100
		logger.Printf("    %-*s: %d (%.1f%%)", reasonWidth, reason, count, percentage)
	}
	logger.Printf("")

	// 答案正确性统计
	if questionsWithAnswer > 0 {
		accuracy := float64(correctCount) / float64(questionsWithAnswer) * 100
		logger.Printf("  Answer Accuracy")
		logger.Printf("  %s", subrule)
		logger.Printf("    %-*s: %d", labelWidth, "Questions with answers", questionsWithAnswer)
		logger.Printf("    %-*s: %d", labelWidth, "Correct answers", correctCount)
		logger.Printf("    %-*s: %d", labelWidth, "Wrong answers", questionsWithAnswer-correctCount)
		logger.Printf("    %-*s: %.2f%%", labelWidth, "Accuracy", accuracy)
		logger.Printf("    By dataset:")
		datasets := slices.Sorted(maps.Keys(datasetQuestionsWithAnswer))
		datasetWidth := util.MaxLen(datasets)
		for _, dataset := range datasets {
			datasetAccuracy := float64(datasetCorrectCount[dataset]) / float64(datasetQuestionsWithAnswer[dataset]) * 100
			logger.Printf("      - %-*s: %d/%d (%.2f%%)", datasetWidth, dataset, datasetCorrectCount[dataset], datasetQuestionsWithAnswer[dataset], datasetAccuracy)
		}
		logger.Printf("")
	}

	// 性能统计
	logger.Printf("  Performance")
	logger.Printf("  %s", subrule)
	logger.Printf("    %-*s: %d ms", labelWidth, "Average TTFT", totalTTFT.Milliseconds()/int64(successCount))
	logger.Printf("    %-*s: %d ms", labelWidth, "Average Total Time", totalTime.Milliseconds()/int64(successCount))
	logger.Printf("    %-*s: %d", labelWidth, "Average Tokens", totalTokens/int64(successCount))
	logger.Printf("    %-*s: %.2f tokens/s (用户感知速度，含 TTFT 摊薄)", labelWidth, "Average E2E TPS", totalTPSE2E/float64(successCount))
	logger.Printf("    %-*s: %.2f", labelWidth, "Average E2E TPM", totalTPME2E/float64(successCount))

	// 解码速率：headline 用总量比率（Σtokens / Σ生成窗口），比率之比对异常值
	// 天然钝感；均值易受残留异常样本影响，仅作参考
	if decodeValidCount > 0 {
		logger.Printf("")
		aggDecodeTPS := float64(decodeTokens) / decodeWindow.Seconds()
		logger.Printf("    %-*s: %.2f tokens/s (%d 个有效样本的 Σtokens/Σ生成窗口)", labelWidth, "Aggregate Decode TPS", aggDecodeTPS, decodeValidCount)
		logger.Printf("    %-*s: %.2f", labelWidth, "Aggregate Decode TPM", aggDecodeTPS*60)
		logger.Printf("    %-*s: %.2f", labelWidth, "Average Decode TPS", totalTPSDecode/float64(decodeValidCount))
	}

	// System TPS/TPM：全部成功问题的 output tokens 总量 / 整批运行的墙钟耗时，
	// 天然反映 MaxWorkers 并发下的真实系统吞吐——与上面 Aggregate Decode TPS
	// 不同，后者是各请求生成窗口时长直接相加做分母，并发越高这些窗口重叠越多，
	// 结果会退化成单流平均解码速度，测不出并发带来的系统吞吐提升
	if elapsed.Seconds() > 0 && totalOutputTokens > 0 {
		logger.Printf("")
		systemTPS := float64(totalOutputTokens) / elapsed.Seconds()
		logger.Printf("    %-*s: %.2f tokens/s (%d 个成功问题的 Σoutput_tokens / 整批运行墙钟耗时 %s)", labelWidth, "System TPS", systemTPS, successCount, elapsed.Round(time.Millisecond))
		logger.Printf("    %-*s: %.2f", labelWidth, "System TPM", systemTPS*60)
	}

	logger.Printf("")
	if totalPromptTokens > 0 {
		logger.Printf("    %-*s: %.2f%%", labelWidth, "Cache Hit Ratio", util.CacheHitRatio(totalCacheTokens, totalPromptTokens))
	} else {
		logger.Printf("    %-*s: n/a", labelWidth, "Cache Hit Ratio")
	}
	logger.Printf("")

	// 附注：数据质量与统计口径上的注意事项，仅在触发时才输出
	notesPrinted := false
	printNote := func(label string, format string, args ...any) {
		if !notesPrinted {
			logger.Printf("  Notes")
			logger.Printf("  %s", subrule)
			notesPrinted = true
		}
		logger.Printf("    %-*s: "+format, append([]any{labelWidth, label}, args...)...)
	}
	if excluded := successCount - decodeValidCount; excluded > 0 {
		printNote("Decode TPS excluded", "%d/%d 条样本未通过速率有效性校验（一次性到达、超单流物理上限或未捕获 TTFT），已从解码速率剔除", excluded, successCount)
	}
	if tokensEstimatedCount > 0 {
		printNote("Tokens estimated", "%d/%d 条样本的 token 数为文本估算（网关未上报 usage），速率可信度下降", tokensEstimatedCount, successCount)
	}
	if reasoningMergedCount > 0 {
		printNote("Reasoning tokens merged", "%d/%d 条样本的网关把 reasoning_tokens 算作独立于 completion_tokens 的计数，已合并进 token 总数，避免思考型模型 TPS 被低估", reasoningMergedCount, successCount)
	}
	if notesPrinted {
		logger.Printf("")
	}

	logger.Printf("%s", rule)
}
