package protocol

import (
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

func TestCheckStopSequence(t *testing.T) {
	t.Run("停止词生效", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(req *params.Request) (*params.Result, error) {
			out := "苹果 香蕉 樱桃 西瓜"
			for _, s := range req.Stop {
				if i := strings.Index(out, s); i >= 0 {
					out = out[:i]
				}
			}
			return &params.Result{Content: out, FinishReason: "stop"}, nil
		}}
		r := checkStopSequence(t.Context(), p)
		if r.Status != types.StatusPass || r.Score != 1 {
			t.Errorf("status=%s score=%v detail=%s, want pass/1", r.Status, r.Score, r.Detail)
		}
	})

	t.Run("停止词被忽略判 fail", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*params.Request) (*params.Result, error) {
			return &params.Result{Content: "苹果 香蕉 樱桃 西瓜", FinishReason: "stop"}, nil
		}}
		r := checkStopSequence(t.Context(), p)
		if r.Status != types.StatusFail {
			t.Errorf("stop 未生效应判 fail, status=%s score=%v", r.Status, r.Score)
		}
	})
}

func TestCheckStreamUsageOptions(t *testing.T) {
	t.Run("两态行为正确", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(req *params.Request) (*params.Result, error) {
			r := &params.Result{Content: "你好", FinishReason: "stop"}
			if req.StreamIncludeUsage == nil || *req.StreamIncludeUsage {
				r.PromptTokens, r.CompletionTokens = 10, 5
			}
			return r, nil
		}}
		r := checkStreamUsageOptions(t.Context(), p)
		if r.Status != types.StatusPass || r.Score != 1 {
			t.Errorf("status=%s score=%v detail=%s, want pass/1", r.Status, r.Score, r.Detail)
		}
	})

	t.Run("include_usage=false 仍返回 usage 判 fail", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*params.Request) (*params.Result, error) {
			return &params.Result{Content: "你好", FinishReason: "stop", PromptTokens: 10, CompletionTokens: 5}, nil
		}}
		r := checkStreamUsageOptions(t.Context(), p)
		if r.Status != types.StatusFail {
			t.Errorf("false 态仍带 usage 应判 fail, status=%s detail=%s", r.Status, r.Detail)
		}
	})
}

func TestCheckThinkingControl(t *testing.T) {
	constraints := &config.ModelConstraints{
		ThinkingEnableParams:  &config.ThinkingParams{Thinking: config.Thinking{Type: config.ThinkingEnabled}},
		ThinkingDisableParams: &config.ThinkingParams{Thinking: config.Thinking{Type: config.ThinkingDisabled}},
	}

	t.Run("开关生效得满分", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(req *params.Request) (*params.Result, error) {
			r := &params.Result{Content: "9.9 更大", FinishReason: "stop", CompletionTokens: 20}
			if th, ok := req.ExtraParams["thinking"].(map[string]any); ok && th["type"] == "enabled" {
				r.ReasoningContent = "比较小数部分：0.11 < 0.9"
				r.CompletionTokens = 200
			}
			return r, nil
		}}
		r := checkThinkingControl(t.Context(), p, constraints)
		if r.Status != types.StatusPass || r.Score != 1 {
			t.Errorf("status=%s score=%v detail=%s, want pass/1", r.Status, r.Score, r.Detail)
		}
	})

	t.Run("关闭后仍思考只得开启分", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*params.Request) (*params.Result, error) {
			return &params.Result{Content: "9.9 更大", ReasoningContent: "思考…",
				FinishReason: "stop", CompletionTokens: 200}, nil
		}}
		r := checkThinkingControl(t.Context(), p, constraints)
		if r.Score >= 1 {
			t.Errorf("关闭开关未生效不应满分, score=%v detail=%s", r.Score, r.Detail)
		}
		if !strings.Contains(r.Detail, "开关未生效") {
			t.Errorf("detail 应说明开关未生效, got %q", r.Detail)
		}
	})

	t.Run("未配置时跳过", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*params.Request) (*params.Result, error) {
			return &params.Result{Content: "ok"}, nil
		}}
		r := checkThinkingControl(t.Context(), p, &config.ModelConstraints{})
		if r.Status != types.StatusSkip {
			t.Errorf("未配置应 skip, status=%s", r.Status)
		}
	})
}

func TestCheckToolResultRoundTrip(t *testing.T) {
	t.Run("二次推理引用结果", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(req *params.Request) (*params.Result, error) {
			for _, m := range req.Messages {
				if m.Role == "tool" {
					return &params.Result{Content: "巴黎现在气温 19°C。", FinishReason: "stop"}, nil
				}
			}
			return &params.Result{FinishReason: "tool_calls",
				ToolCalls: []params.ToolCall{{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`}}}, nil
		}}
		r := checkToolResultRoundTrip(t.Context(), p)
		if r.Status != types.StatusPass || r.Score != 1 {
			t.Errorf("status=%s score=%v detail=%s, want pass/1", r.Status, r.Score, r.Detail)
		}
	})

	t.Run("第一轮无工具调用判 fail", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*params.Request) (*params.Result, error) {
			return &params.Result{Content: "我不知道", FinishReason: "stop"}, nil
		}}
		r := checkToolResultRoundTrip(t.Context(), p)
		if r.Status != types.StatusFail {
			t.Errorf("无工具调用应判 fail, status=%s", r.Status)
		}
	})
}
