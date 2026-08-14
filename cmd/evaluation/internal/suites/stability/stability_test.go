package stability

import (
	"context"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
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

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"巴黎", "巴黎"},
		{"巴黎。", "巴黎"},          // 尾部句号不应算作另一种答案
		{" Paris ", "paris"},   // 空白与大小写
		{"`391`", "391"},       // 代码围栏反引号
		{"391！", "391"},        // 全角感叹号
		{"olleh.", "olleh"},    // 英文句点
		{"  dlrow  ", "dlrow"}, // 前后空白
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// 标点差异不应被计为不同答案（用户真实案例："巴黎" vs "巴黎。"曾被算 2 种答案）。
func TestSelfConsistencyPunctuationVariants(t *testing.T) {
	i := 0
	variants := []string{"巴黎", "巴黎。", "巴黎", "巴黎。", "巴黎"}
	p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
		ans := variants[i%len(variants)]
		i++
		return &provider.Result{Content: ans, FinishReason: "stop"}, nil
	}}
	r := checkSelfConsistency(t.Context(), p, config.StabilityConfig{Samples: 5})
	if r.Status != types.StatusPass || r.Score != 1 {
		t.Errorf("标点差异应视为同一答案, status=%s score=%v detail=%s", r.Status, r.Score, r.Detail)
	}
}

// 空输出不应被计为一种答案，且全空时应判 fail 并说明原因。
func TestSelfConsistencyAllEmpty(t *testing.T) {
	p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
		return &provider.Result{Content: "", FinishReason: "length"}, nil
	}}
	r := checkSelfConsistency(t.Context(), p, config.StabilityConfig{Samples: 3})
	if r.Status != types.StatusFail {
		t.Errorf("全空输出应判 fail, status=%s", r.Status)
	}
	if r.Metrics["empty"].(int) == 0 {
		t.Errorf("应记录空输出次数, metrics=%v", r.Metrics)
	}
}

// 空输出与错误答案应区分统计（用户真实案例：思考型模型预算耗尽输出空串）。
func TestPromptPerturbationEmptyOutput(t *testing.T) {
	p := &fakeProvider{chatFn: func(*provider.Request) (*provider.Result, error) {
		return &provider.Result{Content: "", FinishReason: "length"}, nil
	}}
	r := checkPromptPerturbation(t.Context(), p)
	if r.Status != types.StatusFail || r.Score != 0 {
		t.Errorf("全空输出仍应判 fail, status=%s score=%v", r.Status, r.Score)
	}
	if r.Metrics["empty"].(int) != 3 {
		t.Errorf("应记录 3 次空输出, metrics=%v", r.Metrics)
	}
	if !strings.Contains(r.Detail, "输出为空") || !strings.Contains(r.Detail, "人工复核") {
		t.Errorf("detail 应提示空输出并建议人工复核, got %q", r.Detail)
	}
}

// 内容型检查项应给足预算，避免思考型模型正文为空。
func TestContentBudgetPropagated(t *testing.T) {
	var gotMax int
	p := &fakeProvider{chatFn: func(req *provider.Request) (*provider.Result, error) {
		gotMax = req.MaxTokens
		return &provider.Result{Content: "42", FinishReason: "stop"}, nil
	}}
	checkPromptPerturbation(t.Context(), p)
	if gotMax != contentBudget {
		t.Errorf("prompt_perturbation MaxTokens = %d, want %d", gotMax, contentBudget)
	}
	checkSelfConsistency(t.Context(), p, config.StabilityConfig{Samples: 1})
	if gotMax != contentBudget {
		t.Errorf("self_consistency MaxTokens = %d, want %d", gotMax, contentBudget)
	}
}
