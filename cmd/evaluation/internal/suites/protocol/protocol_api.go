// L2 扩展检查项（对应接入要求 3.2 API 功能测试）：
// stop 停止词、seed 幂等性、stream_options 两态、编码与特殊字符。
package protocol

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
)

// checkStopSequence 验证 stop 停止词生效：输出应在停止词处截断且不含停止词本身。
// 覆盖单个停止词与多个停止词两种场景。服务返回 400 记 unsupported。
func checkStopSequence(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("stop_sequence", 1, func() core.CheckResult {
		const prompt = "一字不差地复述这句话：苹果 香蕉 樱桃 西瓜"
		var points, total float64
		var details []string
		metrics := map[string]any{}

		cases := []struct {
			name string
			stop []string
		}{
			{"single", []string{"樱桃"}},
			{"multiple", []string{"樱桃", "香蕉"}},
		}
		for _, tc := range cases {
			total++
			resp, err := p.Chat(ctx, &provider.Request{
				Messages:  []provider.Message{{Role: "user", Content: prompt}},
				MaxTokens: contentBudget,
				Stop:      tc.stop,
			})
			if err != nil {
				if provider.StatusCode(err) == 400 {
					return core.CheckResult{Status: core.StatusUnsupported,
						Detail: "服务不支持 stop 参数"}
				}
				details = append(details, fmt.Sprintf("%s: 请求失败 %v", tc.name, err))
				continue
			}
			var leaked []string
			for _, s := range tc.stop {
				if strings.Contains(resp.Content, s) {
					leaked = append(leaked, s)
				}
			}
			metrics[tc.name+"_output"] = truncate(resp.Content, 40)
			if len(leaked) == 0 {
				points++
			} else {
				details = append(details, fmt.Sprintf("%s: 输出包含停止词 %v（stop 未生效）", tc.name, leaked))
			}
		}

		score := points / total
		status := core.StatusPass
		if score == 0 {
			status = core.StatusFail
		}
		return core.CheckResult{Status: status, Score: score,
			Detail: strings.Join(details, "; "), Metrics: metrics}
	})
}

// checkSeedConsistency 验证 seed 参数的结果稳定性（3.2 幂等性验证的可测部分）。
// 相同 seed + temperature 下多次采样，一致率作为得分；
// 与 temperature=0 检查同理，服务侧数值抖动不判 fail，参数被接受即通过。
// seed 为 openai/gemini 参数，anthropic 记 skip。
func checkSeedConsistency(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("seed_consistency", 1, func() core.CheckResult {
		if p.Protocol() == "anthropic" {
			return core.CheckResult{Status: core.StatusSkip, Detail: "协议无 seed 参数"}
		}
		const samples = 3
		seed := int64(42)
		temp := 0.7
		req := &provider.Request{
			Messages:    []provider.Message{{Role: "user", Content: "说一个 1 到 1000000 之间的整数，只输出数字"}},
			MaxTokens:   contentBudget,
			Temperature: &temp,
			Seed:        &seed,
		}
		answers := map[string]int{}
		var errs []string
		empty := 0
		for range samples {
			resp, err := p.Chat(ctx, req)
			if err != nil {
				if provider.StatusCode(err) == 400 {
					return core.CheckResult{Status: core.StatusUnsupported,
						Detail: "服务不支持 seed 参数"}
				}
				errs = append(errs, err.Error())
				continue
			}
			ans := strings.TrimSpace(resp.Content)
			if ans == "" {
				empty++
				continue
			}
			answers[ans]++
		}
		if len(answers) == 0 {
			if empty > 0 {
				return failScore(fmt.Sprintf("%d 次采样输出全部为空", empty))
			}
			return failScore("全部请求失败: " + strings.Join(errs, "; "))
		}
		best, total := 0, 0
		for _, n := range answers {
			if n > best {
				best = n
			}
			total += n
		}
		agreement := float64(best) / float64(total)
		detail := fmt.Sprintf("相同 seed=%d 下 %d 次采样全部一致", seed, total)
		if len(answers) > 1 {
			detail = fmt.Sprintf("相同 seed=%d 下 %d 次采样出现 %d 种输出（seed 复现性为尽力而为语义，不判 fail）",
				seed, total, len(answers))
		}
		return core.CheckResult{Status: core.StatusPass, Score: agreement, Detail: detail,
			Metrics: map[string]any{"samples": total, "distinct": len(answers), "agreement": agreement}}
	})
}

// checkStreamUsageOptions 验证 stream_options.include_usage 的两态行为：
// true 时流末尾返回 usage；false 时流中不返回 usage。
// anthropic/gemini 的流式协议恒携带 usage，仅验证 usage 确实存在。
func checkStreamUsageOptions(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("stream_usage_options", 1, func() core.CheckResult {
		req := func(include bool) *provider.Request {
			v := include
			return &provider.Request{
				Messages:           []provider.Message{{Role: "user", Content: "说「你好」。"}},
				MaxTokens:          contentBudget,
				StreamIncludeUsage: &v,
			}
		}

		if p.Protocol() != "openai" {
			resp, err := p.Stream(ctx, req(true))
			if err != nil {
				return failScore("流式请求失败: " + err.Error())
			}
			if resp.PromptTokens > 0 || resp.CompletionTokens > 0 {
				return core.CheckResult{Status: core.StatusPass, Score: 1,
					Detail: "协议流式恒携带 usage（无 include_usage 开关）"}
			}
			return failScore("流式响应未携带 usage")
		}

		var points, total float64
		var details []string
		metrics := map[string]any{}

		// include_usage=true：应携带 usage
		total++
		resp, err := p.Stream(ctx, req(true))
		if err != nil {
			details = append(details, "include_usage=true 请求失败: "+err.Error())
		} else {
			metrics["with_usage_prompt_tokens"] = resp.PromptTokens
			if resp.PromptTokens > 0 || resp.CompletionTokens > 0 {
				points++
			} else {
				details = append(details, "include_usage=true 但流中未返回 usage")
			}
		}

		// include_usage=false：不应携带 usage
		total++
		resp, err = p.Stream(ctx, req(false))
		if err != nil {
			details = append(details, "include_usage=false 请求失败: "+err.Error())
		} else {
			metrics["without_usage_prompt_tokens"] = resp.PromptTokens
			if resp.PromptTokens == 0 && resp.CompletionTokens == 0 {
				points++
			} else {
				details = append(details, "include_usage=false 但流中仍返回了 usage")
			}
		}

		score := points / total
		status := core.StatusPass
		if score < 1 {
			status = core.StatusFail
		}
		return core.CheckResult{Status: status, Score: score,
			Detail: strings.Join(details, "; "), Metrics: metrics}
	})
}

// checkEncodingUnicode 验证编码与特殊字符处理：
// 中/日文与 emoji 正确往返、含控制字符（\n \t）与 BOM 的输入不报错、输出为合法 UTF-8。
func checkEncodingUnicode(ctx context.Context, p provider.Provider) core.CheckResult {
	return timed("encoding_unicode", 1, func() core.CheckResult {
		var points, total float64
		var details []string
		metrics := map[string]any{}

		// 多语言与 emoji 往返：要求逐字复述
		echoCases := []struct{ name, text string }{
			{"cjk", "你好世界·こんにちは"},
			{"emoji", "🚀🎉"},
		}
		for _, tc := range echoCases {
			total++
			resp, err := p.Chat(ctx, &provider.Request{
				Messages:  []provider.Message{{Role: "user", Content: fmt.Sprintf("一字不差地复述：%s", tc.text)}},
				MaxTokens: contentBudget,
			})
			if err != nil {
				details = append(details, fmt.Sprintf("%s: 请求失败 %v", tc.name, err))
				continue
			}
			if !utf8.ValidString(resp.Content) {
				details = append(details, tc.name+": 输出不是合法 UTF-8")
				continue
			}
			if strings.Contains(resp.Content, tc.text) {
				points++
			} else {
				// 复述能力不足属模型问题而非编码缺陷，输出合法即给半分
				points += 0.5
				details = append(details, fmt.Sprintf("%s: 输出未含原文（编码合法，复述偏差）", tc.name))
			}
		}

		// 控制字符与 BOM：请求成功且输出合法即通过
		total++
		resp, err := p.Chat(ctx, &provider.Request{
			Messages: []provider.Message{
				{Role: "user", Content: "\uFEFF第一行\n\t缩进的第二行\n请回答：以上共几行文字？只输出数字。"},
			},
			MaxTokens: contentBudget,
		})
		if err != nil {
			details = append(details, "control_chars: 请求失败 "+err.Error())
		} else if !utf8.ValidString(resp.Content) {
			details = append(details, "control_chars: 输出不是合法 UTF-8")
		} else {
			points++
			metrics["control_chars_output"] = truncate(resp.Content, 20)
		}

		score := points / total
		status := core.StatusPass
		if score < 0.5 {
			status = core.StatusFail
		}
		return core.CheckResult{Status: status, Score: score,
			Detail: strings.Join(details, "; "), Metrics: metrics}
	})
}
