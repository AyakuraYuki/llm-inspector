// Package performance 实现 L5：模型性能评测。
// 包含延迟/吞吐、并发扩展与上下文长度探针。
package performance

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/stats"
)

const benchPrompt = "请用三句话介绍机器学习的基本概念。"

// Run 执行 L5 全部检查。
func Run(ctx context.Context, p provider.Provider, cfg config.PerformanceConfig) core.LayerResult {
	start := time.Now()
	layer := core.LayerResult{ID: "L5", Name: "模型性能", Enabled: true}

	latency, throughput := measureLatencyThroughput(ctx, p, cfg)
	layer.Checks = append(layer.Checks,
		latency,
		throughput,
		checkConcurrency(ctx, p, cfg),
		checkContextProbe(ctx, p, cfg),
	)
	layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return layer
}

// measureLatencyThroughput 用同一批流式请求产出延迟与吞吐两个检查项。
func measureLatencyThroughput(ctx context.Context, p provider.Provider, cfg config.PerformanceConfig) (core.CheckResult, core.CheckResult) {
	start := time.Now()
	ttfts := make([]float64, 0, cfg.Runs)
	totals := make([]float64, 0, cfg.Runs)
	tpsList := make([]float64, 0, cfg.Runs)
	errorsCount := 0

	for i := 0; i < cfg.Runs; i++ {
		resp, err := p.Stream(ctx, &provider.Request{
			Messages:  []provider.Message{{Role: "user", Content: benchPrompt}},
			MaxTokens: 128,
		})
		if err != nil {
			errorsCount++
			continue
		}
		ttft := resp.TTFTMS
		if ttft < 0 {
			ttft = resp.LatencyMS
		}
		ttfts = append(ttfts, ttft)
		totals = append(totals, resp.LatencyMS)
		genSec := (resp.LatencyMS - ttft) / 1000
		if genSec <= 0 {
			genSec = resp.LatencyMS / 1000
		}
		if genSec > 0 {
			tpsList = append(tpsList, float64(estimateTokens(resp))/genSec)
		}
	}
	durationMS := float64(time.Since(start).Microseconds()) / 1000

	latency := core.CheckResult{
		Name: "latency_ttft", Weight: 2, DurationMS: durationMS,
		Metrics: map[string]any{
			"runs":            cfg.Runs,
			"errors":          errorsCount,
			"ttft_p50_ms":     stats.Percentile(ttfts, 50),
			"ttft_p95_ms":     stats.Percentile(ttfts, 95),
			"ttft_p99_ms":     stats.Percentile(ttfts, 99),
			"total_p50_ms":    stats.Percentile(totals, 50),
			"total_p99_ms":    stats.Percentile(totals, 99),
			"slo_ttft_p99_ms": cfg.SLO.TTFTP99MS,
		},
	}
	if len(ttfts) == 0 {
		latency.Status = core.StatusFail
		latency.Score = 0
		latency.Detail = "全部流式请求失败"
	} else {
		p99 := stats.Percentile(ttfts, 99)
		latency.Score = min(1, cfg.SLO.TTFTP99MS/max(p99, 1))
		latency.Status = statusOf(latency.Score)
		latency.Detail = fmt.Sprintf("TTFT P99=%.0fms（SLO %.0fms）", p99, cfg.SLO.TTFTP99MS)
		if errorsCount > 0 {
			latency.Detail += fmt.Sprintf("，%d 次失败", errorsCount)
		}
	}

	throughput := core.CheckResult{
		Name: "throughput", Weight: 2,
		Metrics: map[string]any{
			"tps_mean":    stats.Mean(tpsList),
			"tps_min":     minSlice(tpsList),
			"slo_min_tps": cfg.SLO.MinTokensPerSec,
		},
	}
	if len(tpsList) == 0 {
		throughput.Status = core.StatusFail
		throughput.Score = 0
		throughput.Detail = "无有效吞吐样本"
	} else {
		mean := stats.Mean(tpsList)
		throughput.Score = min(1, mean/cfg.SLO.MinTokensPerSec)
		throughput.Status = statusOf(throughput.Score)
		throughput.Detail = fmt.Sprintf("单流吞吐均值 %.1f tokens/s（SLO %.1f）", mean, cfg.SLO.MinTokensPerSec)
	}
	return latency, throughput
}

// checkConcurrency 在不同并发度下测量聚合吞吐、错误率与 P99 延迟。
func checkConcurrency(ctx context.Context, p provider.Provider, cfg config.PerformanceConfig) core.CheckResult {
	start := time.Now()
	type level struct {
		concurrency int
		tps         float64
		errRate     float64
		p99         float64
	}
	levels := make([]level, 0, len(cfg.Concurrency))

	for _, c := range cfg.Concurrency {
		total := max(c*4, 4)
		latencies := make([]float64, total)
		tokens := make([]float64, total)
		var mu sync.Mutex
		var wg sync.WaitGroup
		jobs := make(chan int)
		wallStart := time.Now()
		for range c {
			wg.Go(func() {
				for i := range jobs {
					s := time.Now()
					resp, err := p.Chat(ctx, &provider.Request{
						Messages:  []provider.Message{{Role: "user", Content: benchPrompt}},
						MaxTokens: 64,
					})
					mu.Lock()
					if err != nil {
						latencies[i] = -1
					} else {
						latencies[i] = float64(time.Since(s).Microseconds()) / 1000
						tokens[i] = float64(estimateTokens(resp))
					}
					mu.Unlock()
				}
			})
		}
		for i := range total {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		wallSec := time.Since(wallStart).Seconds()

		var okLat []float64
		var tokSum float64
		errs := 0
		for i := range total {
			if latencies[i] < 0 {
				errs++
			} else {
				okLat = append(okLat, latencies[i])
				tokSum += tokens[i]
			}
		}
		lv := level{concurrency: c}
		lv.errRate = float64(errs) / float64(total)
		lv.tps = tokSum / wallSec
		lv.p99 = stats.Percentile(okLat, 99)
		levels = append(levels, lv)
	}

	// 评分：每级 = 错误率得分 × 扩展得分（相对单并发吞吐不回退）
	base := levels[0].tps
	var scoreSum float64
	var details []string
	for _, lv := range levels {
		errScore := 1.0
		if lv.errRate > cfg.SLO.MaxErrorRate {
			errScore = 0
		}
		scaleScore := 1.0
		if base > 0 {
			scaleScore = min(1, lv.tps/base)
		}
		scoreSum += errScore * scaleScore
		details = append(details, fmt.Sprintf("c=%d: %.1f tok/s, err %.1f%%, p99 %.0fms",
			lv.concurrency, lv.tps, lv.errRate*100, lv.p99))
	}
	score := scoreSum / float64(len(levels))

	metrics := map[string]any{"levels": details}
	return core.CheckResult{
		Name: "concurrency_scaling", Weight: 2, Status: statusOfThreshold(score, 0.7), Score: score,
		Detail:     strings.Join(details, "; "),
		Metrics:    metrics,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
	}
}

// checkContextProbe 以指数梯度探测实际可用上下文长度，并记录各档延迟。
func checkContextProbe(ctx context.Context, p provider.Provider, cfg config.PerformanceConfig) core.CheckResult {
	var (
		start         = time.Now()
		sizes         []int
		passed        = 0
		maxOK         = 0
		latencyByStep = map[string]float64{}
		stopReason    string
	)

	for s := 1024; s <= cfg.MaxProbeTokens; s *= 2 {
		sizes = append(sizes, s)
	}

	for _, size := range sizes {
		prompt := buildFiller(size) + "\n\n以上是一段无意义文本，无需阅读。请只回复 OK。"
		s := time.Now()
		_, err := p.Chat(ctx, &provider.Request{
			Messages:  []provider.Message{{Role: "user", Content: prompt}},
			MaxTokens: 8,
		})
		latencyByStep[fmt.Sprintf("%d", size)] = float64(time.Since(s).Microseconds()) / 1000
		if err != nil {
			code := provider.StatusCode(err)
			if code >= 400 && code < 500 {
				stopReason = fmt.Sprintf("在 %d tokens 处达到上下文上限（HTTP %d）", size, code)
			} else {
				stopReason = fmt.Sprintf("在 %d tokens 处请求失败: %v", size, err)
			}
			break
		}
		passed++
		maxOK = size
	}

	score := float64(passed) / float64(len(sizes))
	// 全档通过说明从未触及真实上限：只能断言"至少"，不能断言"实测上限"
	var summary string
	if passed == len(sizes) {
		summary = fmt.Sprintf("探测通过 %d/%d 档，上下文至少 %d tokens（已达配置探测上限 max_probe_tokens，真实上限可能更高）",
			passed, len(sizes), maxOK)
	} else {
		summary = fmt.Sprintf("探测通过 %d/%d 档，实测上限约 %d tokens。%s",
			passed, len(sizes), maxOK, stopReason)
	}
	return core.CheckResult{
		Name: "context_probe", Weight: 1, Status: statusOfThreshold(score, 0.5), Score: score,
		Detail: summary,
		Metrics: map[string]any{
			"max_context_ok":     maxOK,
			"latency_by_size_ms": latencyByStep,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
	}
}

// buildFiller 生成约 n tokens 的英文填充文本（按 1 词 ≈ 1.3 token 估算）。
func buildFiller(n int) string {
	const unit = "lorem ipsum dolor sit amet consectetur adipiscing elit " // 8 词
	words := n * 3 / 4
	repeats := max(words/8, 1)
	return strings.Repeat(unit, repeats)
}

// estimateTokens 优先使用 usage；缺失时按 1.5 字符/token 粗估（中文输出）。
func estimateTokens(resp *provider.Result) int64 {
	if resp.CompletionTokens > 0 {
		return resp.CompletionTokens
	}
	n := int64(float64(utf8.RuneCountInString(resp.Content)) / 1.5)
	if n < 1 && resp.Content != "" {
		n = 1
	}
	return n
}

func statusOf(score float64) core.Status {
	if score >= 0.99 {
		return core.StatusPass
	}
	return core.StatusFail
}

func statusOfThreshold(score, threshold float64) core.Status {
	if score >= threshold {
		return core.StatusPass
	}
	return core.StatusFail
}

func minSlice(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
