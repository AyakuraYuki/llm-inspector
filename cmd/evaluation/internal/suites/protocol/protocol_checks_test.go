package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
)

// fakeProvider 用脚本化的响应序列模拟服务行为。
type fakeProvider struct {
	chatFn func(req *provider.Request) (*provider.Result, error)
}

func (f *fakeProvider) Chat(_ context.Context, req *provider.Request) (*provider.Result, error) {
	return f.chatFn(req)
}
func (f *fakeProvider) Stream(_ context.Context, req *provider.Request) (*provider.Result, error) {
	return f.chatFn(req)
}
func (f *fakeProvider) Models(context.Context) ([]string, error) { return []string{"m"}, nil }
func (f *fakeProvider) Model() string                            { return "m" }
func (f *fakeProvider) Protocol() string                         { return "openai" }

func TestCheckMaxTokens(t *testing.T) {
	tests := []struct {
		name       string
		result     *provider.Result
		wantStatus core.Status
		wantScore  float64
	}{
		{"截断标志生效", &provider.Result{Content: "秋风", FinishReason: "length", CompletionTokens: 16}, core.StatusPass, 1},
		{"usage 未超限", &provider.Result{Content: "短诗", FinishReason: "stop", CompletionTokens: 10}, core.StatusPass, 1},
		{"usage 缺失按字符估算", &provider.Result{Content: "短短", FinishReason: "stop"}, core.StatusPass, 1},
		// 用户真实案例：限 16 产出 1021 tokens 且 finish=stop，参数被网关丢弃
		{"参数被丢弃判 fail", &provider.Result{Content: strings.Repeat("长", 3000), FinishReason: "stop", CompletionTokens: 1021}, core.StatusFail, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
				return tt.result, nil
			}}
			r := checkMaxTokens(t.Context(), p)
			if r.Status != tt.wantStatus || r.Score != tt.wantScore {
				t.Errorf("status=%s score=%v, want %s/%v（detail: %s）",
					r.Status, r.Score, tt.wantStatus, tt.wantScore, r.Detail)
			}
		})
	}
}

func TestCheckTemperatureZero(t *testing.T) {
	t.Run("全部一致得满分", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
			return &provider.Result{Content: "42", FinishReason: "stop"}, nil
		}}
		r := checkTemperatureZero(t.Context(), p, nil)
		if r.Status != core.StatusPass || r.Score != 1 {
			t.Errorf("status=%s score=%v, want pass/1", r.Status, r.Score)
		}
	})

	t.Run("数值抖动不判 fail", func(t *testing.T) {
		answers := []string{"482916", "482915", "482916"}
		i := 0
		p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
			ans := answers[i%len(answers)]
			i++
			return &provider.Result{Content: ans, FinishReason: "stop"}, nil
		}}
		r := checkTemperatureZero(t.Context(), p, nil)
		if r.Status != core.StatusPass {
			t.Errorf("数值抖动不应判 fail, status=%s", r.Status)
		}
		want := 2.0 / 3.0
		if r.Score < want-0.01 || r.Score > want+0.01 {
			t.Errorf("score=%v, want ≈%.2f", r.Score, want)
		}
		if !strings.Contains(r.Detail, "数值抖动") {
			t.Errorf("detail 应说明数值抖动, got %q", r.Detail)
		}
	})

	t.Run("全部请求失败判 fail", func(t *testing.T) {
		p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
			return nil, errors.New("connection refused")
		}}
		r := checkTemperatureZero(t.Context(), p, nil)
		if r.Status != core.StatusFail || r.Score != 0 {
			t.Errorf("status=%s score=%v, want fail/0", r.Status, r.Score)
		}
	})
}
