package performance

import (
	"context"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/core"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
)

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

// 全档通过时只能断言"至少"，不能宣称"实测上限"（用户真实案例：
// kimi-k3 全档通过被报告为"实测上限约 32768"，实际远大于此）。
func TestContextProbeAllPassedWording(t *testing.T) {
	p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
		return &provider.Result{Content: "OK", FinishReason: "stop"}, nil
	}}
	r := checkContextProbe(t.Context(), p, config.PerformanceConfig{MaxProbeTokens: 4096})
	if r.Status != core.StatusPass || r.Score != 1 {
		t.Fatalf("全档通过应满分, status=%s score=%v", r.Status, r.Score)
	}
	if !strings.Contains(r.Detail, "至少") || !strings.Contains(r.Detail, "真实上限可能更高") {
		t.Errorf("全档通过的 detail 应说明只是下界, got %q", r.Detail)
	}
	if strings.Contains(r.Detail, "实测上限约") {
		t.Errorf("全档通过不应宣称实测上限, got %q", r.Detail)
	}
}

// 中途失败时才能给出实测上限。
func TestContextProbePartialWording(t *testing.T) {
	p := &fakeProvider{chatFn: func(req *provider.Request) (*provider.Result, error) {
		if len(req.Messages[0].Content) > 3000 { // 约 2048 tokens 档开始失败
			return nil, &provider.HTTPError{StatusCode: 400, Body: "context length exceeded"}
		}
		return &provider.Result{Content: "OK", FinishReason: "stop"}, nil
	}}
	r := checkContextProbe(t.Context(), p, config.PerformanceConfig{MaxProbeTokens: 8192})
	if !strings.Contains(r.Detail, "实测上限约") {
		t.Errorf("中途失败应给出实测上限, got %q", r.Detail)
	}
	if strings.Contains(r.Detail, "至少") {
		t.Errorf("中途失败不应使用'至少'措辞, got %q", r.Detail)
	}
}
