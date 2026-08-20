// Package availability 实现 L1：API 可用性检查。
// 该层为门控层：任何检查项 fail 都会中止后续评测。
package availability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// Run 执行 L1 全部检查。badKeyClient 使用错误 API key 构造，用于验证鉴权错误语义。
func Run(ctx context.Context, p provider.Provider, badKeyClient provider.Provider) types.LayerResult {
	start := time.Now()
	layer := types.LayerResult{ID: "L1", Name: "API 可用性", Enabled: true}

	layer.Checks = append(layer.Checks,
		checkModelsEndpoint(ctx, p),
		checkMinimalChat(ctx, p),
		checkErrorSemantics(ctx, badKeyClient, p),
		checkModelListed(ctx, p),
	)
	layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return layer
}

func timed(name string, weight float64, fn func() types.CheckResult) types.CheckResult {
	start := time.Now()
	r := fn()
	r.Name = name
	r.Weight = weight
	r.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return r
}

// checkModelsEndpoint 验证 GET /models 可达且鉴权通过。
func checkModelsEndpoint(ctx context.Context, p provider.Provider) types.CheckResult {
	return timed("models_endpoint", 2, func() types.CheckResult {
		models, err := p.Models(ctx)
		if err != nil {
			return types.CheckResult{Status: types.StatusFail, Score: 0,
				Detail: "GET /models 失败: " + err.Error()}
		}
		return types.CheckResult{Status: types.StatusPass, Score: 1,
			Metrics: map[string]any{"model_count": len(models)}}
	})
}

// checkMinimalChat 验证最小 chat completion 往返。
func checkMinimalChat(ctx context.Context, p provider.Provider) types.CheckResult {
	return timed("minimal_chat", 2, func() types.CheckResult {
		resp, err := p.Chat(ctx, &params.Request{
			Messages:  []params.Message{{Role: "user", Content: "ping"}},
			MaxTokens: 1,
		})
		if err != nil {
			return types.CheckResult{Status: types.StatusFail, Score: 0,
				Detail: "chat completion 失败: " + err.Error()}
		}
		if resp.FinishReason == "" {
			return types.CheckResult{Status: types.StatusFail, Score: 0,
				Detail: "响应缺少 finish_reason"}
		}
		return types.CheckResult{Status: types.StatusPass, Score: 1,
			Metrics: map[string]any{"finish_reason": resp.FinishReason}}
	})
}

// checkErrorSemantics 验证错误语义：错误 key 与不存在的模型都应被"显式拒绝"。
// 评分是阶梯式的：语义标准得满分；5xx 属于非标准但明确拒绝，得半分并在
// detail 中记录偏差；未拒绝（2xx 等）得 0 分且判 fail。
// 坏 key 的满分码按协议区分：openai/anthropic 为 401/403；
// gemini 对无效 API key 返回 400（API_KEY_INVALID），同样视为标准拒绝。
func checkErrorSemantics(ctx context.Context, badKey, p provider.Provider) types.CheckResult {
	return timed("error_semantics", 1, func() types.CheckResult {
		var points, total float64
		var details []string
		metrics := map[string]any{}
		rejectedAll := true

		badKeyFull := map[int]bool{401: true, 403: true}
		expect := "401/403"
		if p.Protocol() == "gemini" {
			badKeyFull[400] = true
			expect = "400/401/403"
		}

		// 错误 API key：期望显式拒绝
		total++
		_, err := badKey.Chat(ctx, &params.Request{
			Messages:  []params.Message{{Role: "user", Content: "ping"}},
			MaxTokens: 1,
		})
		code := provider.StatusCode(err)
		if err == nil {
			code = 200 // 请求成功：对错误语义而言等价于"未被拒绝"
		}
		metrics["bad_key_status"] = code
		switch {
		case badKeyFull[code]:
			points++
		case code >= 400 && code < 500:
			points += 0.5
			details = append(details, fmt.Sprintf("错误 key 返回 %d（期望 %s）", code, expect))
		default:
			rejectedAll = false
			details = append(details, fmt.Sprintf("错误 key 未被正确拒绝（状态码 %d）", code))
		}

		// 不存在的模型：期望 4xx；网关类平台常返回 5xx，视为非标准但显式拒绝
		total++
		_, err = p.Chat(ctx, &params.Request{
			Model:     "nonexistent-model-00000000",
			Messages:  []params.Message{{Role: "user", Content: "ping"}},
			MaxTokens: 1,
		})
		code = provider.StatusCode(err)
		if err == nil {
			code = 200
		}
		metrics["bad_model_status"] = code
		switch {
		case code >= 400 && code < 500:
			points++
		case code >= 500:
			points += 0.5
			details = append(details, fmt.Sprintf("不存在的模型返回 %d（期望 4xx；5xx 为网关式显式拒绝）", code))
		default:
			rejectedAll = false
			details = append(details, fmt.Sprintf("不存在的模型未被拒绝（状态码 %d）", code))
		}

		score := points / total
		status := types.StatusPass
		if !rejectedAll {
			status = types.StatusFail
		}
		metrics["score_detail"] = fmt.Sprintf("%.1f/%.0f", points, total)
		return types.CheckResult{Status: status, Score: score,
			Detail:  strings.Join(details, "; "),
			Metrics: metrics}
	})
}

// checkModelListed 验证目标模型出现在 /models 列表中（若端点可用且非空）。
func checkModelListed(ctx context.Context, p provider.Provider) types.CheckResult {
	return timed("model_listed", 1, func() types.CheckResult {
		models, err := p.Models(ctx)
		if err != nil || len(models) == 0 {
			return types.CheckResult{Status: types.StatusSkip, Score: 0,
				Detail: "/models 不可用或返回空列表"}
		}
		for _, m := range models {
			if m == p.Model() {
				return types.CheckResult{Status: types.StatusPass, Score: 1,
					Detail: "模型在 /models 列表中"}
			}
		}
		return types.CheckResult{Status: types.StatusFail, Score: 0,
			Detail:  "目标模型不在 /models 列表中",
			Metrics: map[string]any{"available": models}}
	})
}
