// Package stability 实现 L4：稳定性评测。
// 包含自一致性、prompt 扰动、浸测与对抗输入四项检查。
package stability

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/stats"
)

// contentBudget 是需要验证输出内容的检查项的 max_tokens 预算。
// 思考型模型会先消耗 completion tokens 做思考，预算过小时正文为空串，
// 会把"预算不足"误判成"输出不稳定/回答错误"。
const contentBudget = 1024

// Run 执行 L4 全部检查。
func Run(ctx context.Context, p provider.Provider, cfg config.StabilityConfig) core.LayerResult {
	start := time.Now()
	layer := core.LayerResult{ID: "L4", Name: "稳定性", Enabled: true}

	layer.Checks = append(layer.Checks,
		checkSelfConsistency(ctx, p, cfg),
		checkPromptPerturbation(ctx, p),
		checkSoak(ctx, p, cfg),
		checkAdversarial(ctx, p),
	)
	layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return layer
}

func timed(name string, weight float64, fn func() core.CheckResult) core.CheckResult {
	start := time.Now()
	r := fn()
	r.Name = name
	r.Weight = weight
	r.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return r
}

// 自一致性探针：有唯一确定答案的问题。
var consistencyProbes = []string{
	"计算 23*17，只输出数字",
	"把字符串「world」反转，只输出结果",
	"法国的首都是哪里？只输出城市名",
}

// checkSelfConsistency 同一问题采样 N 次，统计最频答案占比（答案一致率）。
// 答案经宽松归一化（去空白/大小写/收尾标点/代码围栏），"巴黎"与"巴黎。"视为同一答案；
// 空输出计为异常而非一种答案，避免稀释或抬高一致率。
func checkSelfConsistency(ctx context.Context, p provider.Provider, cfg config.StabilityConfig) core.CheckResult {
	return timed("self_consistency", 2, func() core.CheckResult {
		agreements := make([]float64, 0, len(consistencyProbes))
		var details []string
		errorsCount := 0
		emptyCount := 0

		for _, probe := range consistencyProbes {
			answers := map[string]int{}
			for i := 0; i < cfg.Samples; i++ {
				resp, err := p.Chat(ctx, &provider.Request{
					Messages:    []provider.Message{{Role: "user", Content: probe}},
					MaxTokens:   contentBudget,
					Temperature: cfg.Temperature,
				})
				if err != nil {
					errorsCount++
					continue
				}
				ans := normalize(resp.Content)
				if ans == "" {
					emptyCount++
					continue
				}
				answers[ans]++
			}
			if len(answers) == 0 {
				continue
			}
			best := 0
			for _, n := range answers {
				if n > best {
					best = n
				}
			}
			total := 0
			for _, n := range answers {
				total += n
			}
			agreement := float64(best) / float64(total)
			agreements = append(agreements, agreement)
			if len(answers) > 1 {
				details = append(details, fmt.Sprintf("%q 出现 %d 种答案", truncate(probe, 20), len(answers)))
			}
		}

		if len(agreements) == 0 {
			return core.CheckResult{Status: core.StatusFail, Score: 0,
				Detail: fmt.Sprintf("无有效采样（%d 次请求失败，%d 次输出为空）", errorsCount, emptyCount),
				Metrics: map[string]any{
					"samples":        cfg.Samples,
					"agreement_mean": 0.0,
					"empty":          emptyCount,
					"errors":         errorsCount,
				}}
		}
		score := stats.Mean(agreements)
		status := core.StatusPass
		if score < 0.8 {
			status = core.StatusFail
		}
		if emptyCount > 0 {
			details = append(details, fmt.Sprintf("%d 次输出为空（未计入一致率）", emptyCount))
		}
		if errorsCount > 0 {
			details = append(details, fmt.Sprintf("%d 次请求失败", errorsCount))
		}
		return core.CheckResult{Status: status, Score: score, Detail: strings.Join(details, "; "),
			Metrics: map[string]any{
				"samples":        cfg.Samples,
				"agreement_mean": score,
				"empty":          emptyCount,
				"errors":         errorsCount,
			}}
	})
}

// 同义扰动：三种措辞问同一个问题，答案应一致。
var perturbationPrompts = []string{
	"15 加 27 等于多少？只输出数字",
	"请计算 15 + 27 的和，答案只写数字",
	"What is 15 + 27? Reply with the number only.",
}

// checkPromptPerturbation 同义改写下答案应保持正确。
// 空输出单独记录（思考型模型预算被耗尽时正文为空，与"回答错误"是不同问题）。
func checkPromptPerturbation(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("prompt_perturbation", 1, func() core.CheckResult {
		correct := 0
		empty := 0
		var details []string
		for _, q := range perturbationPrompts {
			resp, err := p.Chat(ctx, &provider.Request{
				Messages:  []provider.Message{{Role: "user", Content: q}},
				MaxTokens: contentBudget,
			})
			if err != nil {
				details = append(details, "请求失败: "+err.Error())
				continue
			}
			switch {
			case strings.Contains(resp.Content, "42"):
				correct++
			case strings.TrimSpace(resp.Content) == "":
				empty++
				details = append(details, fmt.Sprintf("%q → 输出为空（finish_reason=%q）", truncate(q, 24), resp.FinishReason))
			default:
				details = append(details, fmt.Sprintf("%q → %q", truncate(q, 24), truncate(resp.Content, 24)))
			}
		}
		score := float64(correct) / float64(len(perturbationPrompts))
		status := core.StatusPass
		if score < 1 {
			status = core.StatusFail
		}
		if empty == len(perturbationPrompts) {
			details = append(details, "全部输出为空：更可能是服务/预算问题而非扰动敏感，建议人工复核")
		}
		return core.CheckResult{Status: status, Score: score, Detail: strings.Join(details, "; "),
			Metrics: map[string]any{"correct": correct, "empty": empty, "total": len(perturbationPrompts)}}
	})
}

// checkSoak 连续 M 次请求，统计错误率与首尾延迟漂移。
func checkSoak(ctx context.Context, p provider.Provider, cfg config.StabilityConfig) core.CheckResult {
	return timed("soak_test", 2, func() core.CheckResult {
		latencies := make([]float64, 0, cfg.SoakRequests)
		errorsCount := 0
		for i := 0; i < cfg.SoakRequests; i++ {
			start := time.Now()
			_, err := p.Chat(ctx, &provider.Request{
				Messages:  []provider.Message{{Role: "user", Content: "回复 ok"}},
				MaxTokens: 2,
			})
			if err != nil {
				errorsCount++
				continue
			}
			latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
		}

		errRate := float64(errorsCount) / float64(cfg.SoakRequests)
		var drift float64
		const window = 10
		if len(latencies) >= window*2 {
			first := stats.Mean(latencies[:window])
			last := stats.Mean(latencies[len(latencies)-window:])
			if first > 0 {
				drift = (last - first) / first
			}
		}

		// 评分：错误率线性扣分；漂移超过 20% 后追加扣分
		score := 1 - errRate
		if drift > 0.2 {
			score -= (drift - 0.2)
			if score < 0 {
				score = 0
			}
		}
		status := core.StatusPass
		if errRate > 0.01 || drift > 0.5 {
			status = core.StatusFail
		}
		return core.CheckResult{Status: status, Score: score,
			Detail: fmt.Sprintf("错误率 %.1f%%，延迟漂移 %+.1f%%", errRate*100, drift*100),
			Metrics: map[string]any{
				"requests":    cfg.SoakRequests,
				"error_rate":  errRate,
				"drift_ratio": drift,
				"p50_ms":      stats.Percentile(latencies, 50),
				"p99_ms":      stats.Percentile(latencies, 99),
			}}
	})
}

// checkAdversarial 空输入/超长输入/乱码输入应被优雅处理（正常应答或干净的 4xx）。
func checkAdversarial(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("adversarial_inputs", 1, func() core.CheckResult {
		inputs := []struct {
			name    string
			content string
		}{
			{"empty", ""},
			{"long_input", strings.Repeat("数据", 25000)}, // 约 5 万字符
			{"garbage", randomRunes(2000)},
		}
		handled := 0
		var details []string
		for _, in := range inputs {
			_, err := p.Chat(ctx, &provider.Request{
				Messages:  []provider.Message{{Role: "user", Content: in.content}},
				MaxTokens: 8,
			})
			if err == nil {
				handled++
				continue
			}
			code := provider.StatusCode(err)
			if code >= 400 && code < 500 {
				handled++ // 干净的客户端错误属于优雅拒绝
				continue
			}
			details = append(details, fmt.Sprintf("%s 未优雅处理: %v", in.name, err))
		}
		score := float64(handled) / float64(len(inputs))
		status := core.StatusPass
		if score < 1 {
			status = core.StatusFail
		}
		return core.CheckResult{Status: status, Score: score, Detail: strings.Join(details, "; "),
			Metrics: map[string]any{"handled": handled, "total": len(inputs)}}
	})
}

func randomRunes(n int) string {
	rng := rand.New(rand.NewSource(42)) // 固定种子，保证可复现
	var sb strings.Builder
	for sb.Len() < n {
		sb.WriteRune(rune(rng.Intn(0x3000) + 0x20))
	}
	return sb.String()
}

// normalize 对答案做宽松归一化（口径与 internal/scorer 的 normalize 一致）：
// 去空白、去代码围栏反引号、去收尾标点、统一小写。
// "巴黎"与"巴黎。"、"Paris"与"paris"应视为同一答案，避免把排版差异算成不稳定。
func normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "。．.!！?？;；,，")
	return strings.ToLower(strings.TrimSpace(s))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
