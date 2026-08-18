// Package boundary 实现 L6：参数边界与健壮性检查。
// 与 L2（合法请求被正确处理）互补，L6 验证"非法请求被显式拒绝"：
// 通过 RawCaller 直接发送畸形负载，绕过 SDK 的强类型校验。
// 判定口径与 L1 的 error_semantics 一致：4xx 为标准拒绝（满分）；
// 5xx 为非标准但显式拒绝（半分）；2xx 接受非法输入判 fail。
package boundary

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// Run 执行 L6 全部检查。目标 provider 未实现 RawCaller 时整层跳过。
func Run(ctx context.Context, p provider.Provider) types.LayerResult {
	start := time.Now()
	layer := types.LayerResult{ID: "L6", Name: "参数边界与健壮性", Enabled: true}

	raw, ok := p.(provider.RawCaller)
	if !ok {
		layer.Checks = append(layer.Checks, types.CheckResult{
			Name: "raw_capability", Status: types.StatusSkip,
			Detail: "provider 未实现 RawCaller，无法发送裸请求",
		})
		layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
		return layer
	}

	layer.Checks = append(layer.Checks,
		checkMessagesBoundary(ctx, p, raw),
		checkTopPBoundary(ctx, p, raw),
		checkFrequencyPenaltyBoundary(ctx, p, raw),
		checkPresencePenaltyBoundary(ctx, p, raw),
		checkTemperatureBoundary(ctx, p, raw),
		checkMaxTokensBoundary(ctx, p, raw),
		checkMaxCompletionTokensCompat(ctx, p),
		checkAuthBoundary(ctx, p, raw),
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

// basePayload 构造各协议的最小合法请求体。
func basePayload(p provider.Provider) map[string]any {
	switch p.Protocol() {
	case "anthropic":
		return map[string]any{
			"model":      p.Model(),
			"max_tokens": 8,
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
		}
	case "gemini":
		return map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "ping"}}}},
			"generationConfig": map[string]any{"maxOutputTokens": 8},
		}
	default: // openai
		return map[string]any{
			"model":      p.Model(),
			"messages":   []any{map[string]any{"role": "user", "content": "ping"}},
			"max_tokens": 8,
		}
	}
}

// setParam 把采样参数写入协议对应位置：openai/anthropic 在顶层，
// gemini 在 generationConfig 内且命名不同（由调用方传入 geminiKey）。
func setParam(payload map[string]any, protocol, key, geminiKey string, v any) {
	if protocol == "gemini" {
		gc, _ := payload["generationConfig"].(map[string]any)
		if gc == nil {
			gc = map[string]any{}
			payload["generationConfig"] = gc
		}
		gc[geminiKey] = v
		return
	}
	payload[key] = v
}

// probe 是一次裸请求探针：expectReject 表示该负载应被服务显式拒绝。
type probe struct {
	payload      map[string]any
	name         string
	expectReject bool
}

// runProbes 逐个执行探针并按阶梯口径打分。
// 应拒绝的探针：4xx 满分；5xx 半分；2xx 得 0 且整项 fail。
// 应接受的探针（合法值 sanity）：2xx 满分；否则得 0 且整项 fail——
// 服务连合法值都拒绝时，"拒绝了非法值"不构成任何证据。
func runProbes(ctx context.Context, raw provider.RawCaller, probes []probe) types.CheckResult {
	var points, total float64
	var details []string
	metrics := map[string]any{}
	failed := false

	for _, pb := range probes {
		total++
		resp, err := raw.RawChat(ctx, &provider.RawRequest{Payload: pb.payload})
		if err != nil {
			failed = true
			details = append(details, fmt.Sprintf("%s: 请求错误 %v", pb.name, err))
			metrics[pb.name] = "error"
			continue
		}
		metrics[pb.name] = resp.StatusCode
		if pb.expectReject {
			switch {
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				points++
			case resp.StatusCode >= 500:
				points += 0.5
				details = append(details, fmt.Sprintf("%s: 返回 %d（期望 4xx；5xx 为非标准显式拒绝）", pb.name, resp.StatusCode))
			default:
				failed = true
				details = append(details, fmt.Sprintf("%s: 非法输入未被拒绝（状态码 %d）", pb.name, resp.StatusCode))
			}
		} else {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				points++
			} else {
				failed = true
				details = append(details, fmt.Sprintf("%s: 合法值被拒绝（状态码 %d），本项边界判定不可信", pb.name, resp.StatusCode))
			}
		}
	}

	score := 0.0
	if total > 0 {
		score = points / total
	}
	status := types.StatusPass
	if failed {
		status = types.StatusFail
	}
	metrics["score_detail"] = fmt.Sprintf("%.1f/%.0f", points, total)
	return types.CheckResult{Status: status, Score: score,
		Detail: strings.Join(details, "; "), Metrics: metrics}
}

// withParam 复制 base 并设置一个采样参数。
func withParam(p provider.Provider, key, geminiKey string, v any) map[string]any {
	payload := basePayload(p)
	setParam(payload, p.Protocol(), key, geminiKey, v)
	return payload
}

// checkMessagesBoundary 验证 messages 数组的元素与空值边界：
// 空数组、非法 role、null content 均应被拒绝。
func checkMessagesBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("messages_boundary", 2, func() types.CheckResult {
		probes := []probe{
			{name: "valid_minimal", payload: basePayload(p), expectReject: false},
		}
		switch p.Protocol() {
		case "gemini":
			empty := basePayload(p)
			empty["contents"] = []any{}
			badRole := basePayload(p)
			badRole["contents"] = []any{map[string]any{"role": "robot", "parts": []any{map[string]any{"text": "hi"}}}}
			nullParts := basePayload(p)
			nullParts["contents"] = []any{map[string]any{"role": "user", "parts": nil}}
			probes = append(probes,
				probe{name: "empty_contents", payload: empty, expectReject: true},
				probe{name: "invalid_role", payload: badRole, expectReject: true},
				probe{name: "null_parts", payload: nullParts, expectReject: true},
			)
		default: // openai / anthropic 的 messages 结构一致
			empty := basePayload(p)
			empty["messages"] = []any{}
			noRole := basePayload(p)
			noRole["messages"] = []any{map[string]any{"content": "hi"}}
			badRole := basePayload(p)
			badRole["messages"] = []any{map[string]any{"role": "robot", "content": "hi"}}
			nullContent := basePayload(p)
			nullContent["messages"] = []any{map[string]any{"role": "user", "content": nil}}
			probes = append(probes,
				probe{name: "empty_messages", payload: empty, expectReject: true},
				probe{name: "missing_role", payload: noRole, expectReject: true},
				probe{name: "invalid_role", payload: badRole, expectReject: true},
				probe{name: "null_content", payload: nullContent, expectReject: true},
			)
		}
		return runProbes(ctx, raw, probes)
	})
}

// checkTopPBoundary 验证 top_p 的范围与类型边界：[0,1] 内合法，越界与类型错误应被拒绝。
func checkTopPBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("top_p_boundary", 1, func() types.CheckResult {
		return runProbes(ctx, raw, []probe{
			{name: "valid_0.5", payload: withParam(p, "top_p", "topP", 0.5), expectReject: false},
			{name: "over_range_1.5", payload: withParam(p, "top_p", "topP", 1.5), expectReject: true},
			{name: "negative_-0.5", payload: withParam(p, "top_p", "topP", -0.5), expectReject: true},
			{name: "wrong_type_string", payload: withParam(p, "top_p", "topP", "abc"), expectReject: true},
		})
	})
}

// checkFrequencyPenaltyBoundary 验证 frequency_penalty 的 [-2,2] 边界（openai 专属参数）。
func checkFrequencyPenaltyBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("frequency_penalty_boundary", 1, func() types.CheckResult {
		if p.Protocol() != "openai" {
			return types.CheckResult{Status: types.StatusSkip, Detail: "协议无 frequency_penalty 参数"}
		}
		return runProbes(ctx, raw, []probe{
			{name: "valid_2", payload: withParam(p, "frequency_penalty", "", 2), expectReject: false},
			{name: "valid_-2", payload: withParam(p, "frequency_penalty", "", -2), expectReject: false},
			{name: "over_range_3", payload: withParam(p, "frequency_penalty", "", 3), expectReject: true},
			{name: "under_range_-3", payload: withParam(p, "frequency_penalty", "", -3), expectReject: true},
			{name: "wrong_type_string", payload: withParam(p, "frequency_penalty", "", "high"), expectReject: true},
		})
	})
}

// checkPresencePenaltyBoundary 验证 presence_penalty 的 [-2,2] 边界，
// 并覆盖与 frequency_penalty 的叠加场景（openai 专属参数）。
func checkPresencePenaltyBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("presence_penalty_boundary", 1, func() types.CheckResult {
		if p.Protocol() != "openai" {
			return types.CheckResult{Status: types.StatusSkip, Detail: "协议无 presence_penalty 参数"}
		}
		combined := withParam(p, "presence_penalty", "", 1.5)
		combined["frequency_penalty"] = 1.5
		return runProbes(ctx, raw, []probe{
			{name: "valid_2", payload: withParam(p, "presence_penalty", "", 2), expectReject: false},
			{name: "combined_with_frequency", payload: combined, expectReject: false},
			{name: "over_range_3", payload: withParam(p, "presence_penalty", "", 3), expectReject: true},
			{name: "wrong_type_bool", payload: withParam(p, "presence_penalty", "", true), expectReject: true},
		})
	})
}

// checkTemperatureBoundary 验证 temperature 的范围与类型边界。
// openai/gemini 合法上限 2，anthropic 为 1；越界值与类型错误应被拒绝。
func checkTemperatureBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("temperature_boundary", 1, func() types.CheckResult {
		over := 3.0 // openai/gemini 的越界探针
		if p.Protocol() == "anthropic" {
			over = 1.5
		}
		return runProbes(ctx, raw, []probe{
			{name: "valid_1", payload: withParam(p, "temperature", "temperature", 1), expectReject: false},
			{name: fmt.Sprintf("over_range_%.1f", over), payload: withParam(p, "temperature", "temperature", over), expectReject: true},
			{name: "negative_-1", payload: withParam(p, "temperature", "temperature", -1), expectReject: true},
			{name: "wrong_type_string", payload: withParam(p, "temperature", "temperature", "hot"), expectReject: true},
		})
	})
}

// checkMaxTokensBoundary 验证 max_tokens 的边界值：0、负数、极大值、类型错误。
// 0 与极大值的处理各实现差异较大：显式拒绝（4xx）满分；
// 静默容忍（200）对极大值记半分并注明（属容错而非缺陷），对 0 判非标准。
func checkMaxTokensBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("max_tokens_boundary", 1, func() types.CheckResult {
		key, gkey := "max_tokens", "maxOutputTokens"
		var points, total float64
		var details []string
		metrics := map[string]any{}
		failed := false

		// 负数与类型错误：必须拒绝
		strict := []probe{
			{name: "negative_-1", payload: withParam(p, key, gkey, -1)},
			{name: "wrong_type_string", payload: withParam(p, key, gkey, "many")},
		}
		for _, pb := range strict {
			total++
			resp, err := raw.RawChat(ctx, &provider.RawRequest{Payload: pb.payload})
			if err != nil {
				failed = true
				details = append(details, fmt.Sprintf("%s: 请求错误 %v", pb.name, err))
				continue
			}
			metrics[pb.name] = resp.StatusCode
			switch {
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				points++
			case resp.StatusCode >= 500:
				points += 0.5
				details = append(details, fmt.Sprintf("%s: 返回 %d（期望 4xx）", pb.name, resp.StatusCode))
			default:
				failed = true
				details = append(details, fmt.Sprintf("%s: 非法值未被拒绝（状态码 %d）", pb.name, resp.StatusCode))
			}
		}

		// 0：标准实现拒绝（4xx 满分）；接受则记半分并注明
		total++
		resp, err := raw.RawChat(ctx, &provider.RawRequest{Payload: withParam(p, key, gkey, 0)})
		if err == nil {
			metrics["zero"] = resp.StatusCode
			switch {
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				points++
			case resp.StatusCode < 300:
				points += 0.5
				details = append(details, "max_tokens=0 被接受（非标准，多数实现应拒绝）")
			default:
				points += 0.5
				details = append(details, fmt.Sprintf("max_tokens=0 返回 %d", resp.StatusCode))
			}
		} else {
			failed = true
			details = append(details, "zero: 请求错误 "+err.Error())
		}

		// 极大值：显式拒绝满分；静默容忍（截断到上限）半分
		total++
		resp, err = raw.RawChat(ctx, &provider.RawRequest{Payload: withParam(p, key, gkey, 1_000_000_000)})
		if err == nil {
			metrics["huge_1e9"] = resp.StatusCode
			switch {
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				points++
			case resp.StatusCode < 300:
				points += 0.5
				details = append(details, "极大 max_tokens 被静默容忍（属容错行为，建议显式报错）")
			default:
				points += 0.5
				details = append(details, fmt.Sprintf("极大 max_tokens 返回 %d", resp.StatusCode))
			}
		} else {
			failed = true
			details = append(details, "huge: 请求错误 "+err.Error())
		}

		score := points / total
		status := types.StatusPass
		if failed {
			status = types.StatusFail
		}
		metrics["score_detail"] = fmt.Sprintf("%.1f/%.0f", points, total)
		return types.CheckResult{Status: status, Score: score,
			Detail: strings.Join(details, "; "), Metrics: metrics}
	})
}

// checkMaxCompletionTokensCompat 验证 openai 的 max_completion_tokens 兼容字段
// 被接受且与 max_tokens 同语义（限制生效）。服务返回 400 记 unsupported。
func checkMaxCompletionTokensCompat(ctx context.Context, p provider.Provider) types.CheckResult {
	return timed("max_completion_tokens_compat", 1, func() types.CheckResult {
		if p.Protocol() != "openai" {
			return types.CheckResult{Status: types.StatusSkip, Detail: "openai 专属兼容字段"}
		}
		const limit = 16
		resp, err := p.Chat(ctx, &provider.Request{
			Messages:            []provider.Message{{Role: "user", Content: "写一首关于秋天的长诗，越长越好"}},
			MaxCompletionTokens: limit,
		})
		if err != nil {
			if provider.StatusCode(err) == 400 {
				return types.CheckResult{Status: types.StatusUnsupported,
					Detail: "服务不支持 max_completion_tokens"}
			}
			return types.CheckResult{Status: types.StatusFail, Detail: "请求失败: " + err.Error()}
		}
		if resp.FinishReason == "length" {
			return types.CheckResult{Status: types.StatusPass, Score: 1,
				Detail: "finish_reason=length，max_completion_tokens 生效"}
		}
		used := resp.CompletionTokens
		if used <= 0 {
			used = int64(float64(len([]rune(resp.Content))) / 1.5)
		}
		if used <= int64(limit) {
			return types.CheckResult{Status: types.StatusPass, Score: 1,
				Detail: fmt.Sprintf("输出约 %d tokens，未超限", used)}
		}
		return types.CheckResult{Status: types.StatusFail,
			Detail: fmt.Sprintf("max_completion_tokens=%d 未生效：输出 %d tokens", limit, used)}
	})
}

// checkAuthBoundary 验证鉴权边界：未携带凭据与畸形凭据都应被拒绝。
// 标准码：openai/anthropic 401（403 也接受）；gemini 缺 key 常见 401/403，
// 畸形 key 为 400（API_KEY_INVALID），均视为标准。
func checkAuthBoundary(ctx context.Context, p provider.Provider, raw provider.RawCaller) types.CheckResult {
	return timed("auth_boundary", 2, func() types.CheckResult {
		fullCodes := map[int]bool{401: true, 403: true}
		expect := "401/403"
		if p.Protocol() == "gemini" {
			fullCodes[400] = true
			expect = "400/401/403"
		}

		var points, total float64
		var details []string
		metrics := map[string]any{}
		failed := false
		for _, pb := range []struct {
			name string
			req  *provider.RawRequest
		}{
			{"no_auth", &provider.RawRequest{Payload: basePayload(p), OmitAuth: true}},
			{"malformed_token", &provider.RawRequest{Payload: basePayload(p), OverrideAuth: "not-a-valid-token-!!!"}},
		} {
			total++
			resp, err := raw.RawChat(ctx, pb.req)
			if err != nil {
				failed = true
				details = append(details, fmt.Sprintf("%s: 请求错误 %v", pb.name, err))
				continue
			}
			metrics[pb.name] = resp.StatusCode
			switch {
			case fullCodes[resp.StatusCode]:
				points++
			case resp.StatusCode >= 400 && resp.StatusCode < 500:
				points += 0.5
				details = append(details, fmt.Sprintf("%s: 返回 %d（期望 %s）", pb.name, resp.StatusCode, expect))
			default:
				failed = true
				details = append(details, fmt.Sprintf("%s: 未被拒绝（状态码 %d）", pb.name, resp.StatusCode))
			}
		}

		score := points / total
		status := types.StatusPass
		if failed {
			status = types.StatusFail
		}
		metrics["score_detail"] = fmt.Sprintf("%.1f/%.0f", points, total)
		return types.CheckResult{Status: status, Score: score,
			Detail: strings.Join(details, "; "), Metrics: metrics}
	})
}
