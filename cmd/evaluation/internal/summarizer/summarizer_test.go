package summarizer

import (
	"context"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// fakeProvider 实现 provider.Provider，Chat 返回预置结果。
type fakeProvider struct {
	resp *params.Result
	err  error
}

func (f *fakeProvider) Chat(_ context.Context, _ *params.Request) (*params.Result, error) {
	return f.resp, f.err
}
func (f *fakeProvider) Stream(_ context.Context, _ *params.Request) (*params.Result, error) {
	return f.resp, f.err
}
func (f *fakeProvider) Models(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeProvider) Model() string                              { return "judge-model" }
func (f *fakeProvider) Protocol() string                           { return "openai" }

func buildReport() *types.Report {
	return &types.Report{
		Target: types.TargetInfo{Model: "mock-model", Protocol: "openai"},
		Sections: []types.SectionResult{
			{Section: types.SectionAccess, Status: "pass", Score: 0.92},
			{Section: types.SectionPerf, Status: "warn", Score: 0.7},
			{Section: types.SectionSmoke, Status: "pass", Score: 0.88},
		},
		Layers: []types.LayerResult{
			{ID: "L5", Name: "模型性能", Checks: []types.CheckResult{
				{Name: "latency_ttft", Metrics: map[string]any{"ttft_p99_ms": float64(1850), "slo_ttft_p99_ms": float64(2000)}},
			}},
		},
	}
}

func TestSummarizeNormal(t *testing.T) {
	s := New(&fakeProvider{resp: &params.Result{Content: "该模型可以接入，性能需关注。"}})
	text, err := s.Summarize(context.Background(), buildReport())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !strings.Contains(text, "可以接入") {
		t.Errorf("总结应包含正文，实际 %q", text)
	}
}

func TestSummarizeStripFence(t *testing.T) {
	s := New(&fakeProvider{resp: &params.Result{Content: "```text\n该模型可用。\n```"}})
	text, err := s.Summarize(context.Background(), buildReport())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if strings.Contains(text, "```") {
		t.Errorf("应去除代码围栏，实际 %q", text)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	s := New(&fakeProvider{resp: &params.Result{Content: ""}})
	if _, err := s.Summarize(context.Background(), buildReport()); err == nil {
		t.Error("空输出应返回 error")
	}
}

func TestSummarizeTruncate(t *testing.T) {
	long := strings.Repeat("长", 500)
	s := New(&fakeProvider{resp: &params.Result{Content: long}})
	text, err := s.Summarize(context.Background(), buildReport())
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len([]rune(text)) > 301 { // 300 + 省略号
		t.Errorf("总结超长：%d 字", len([]rune(text)))
	}
}

func TestNewNil(t *testing.T) {
	if New(nil) != nil {
		t.Error("New(nil) 应返回 nil")
	}
}
