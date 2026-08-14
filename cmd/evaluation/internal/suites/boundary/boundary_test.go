package boundary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// fakeRawProvider 用可编程的规则模拟服务端对裸请求的判定。
type fakeRawProvider struct {
	protocol string
	// rawFn 返回状态码；nil 时默认对"合法负载"返回 200、其余 400。
	rawFn func(req *provider.RawRequest) int
}

func (f *fakeRawProvider) Chat(context.Context, *provider.Request) (*provider.Result, error) {
	return &provider.Result{Content: "ok", FinishReason: "stop", CompletionTokens: 4}, nil
}
func (f *fakeRawProvider) Stream(ctx context.Context, req *provider.Request) (*provider.Result, error) {
	return f.Chat(ctx, req)
}
func (f *fakeRawProvider) Models(context.Context) ([]string, error) { return []string{"m"}, nil }
func (f *fakeRawProvider) Model() string                            { return "m" }
func (f *fakeRawProvider) Protocol() string                         { return f.protocol }
func (f *fakeRawProvider) RawChat(_ context.Context, req *provider.RawRequest) (*provider.RawResult, error) {
	// 模拟真实链路的 JSON 序列化：int 等 Go 原生类型统一变为 float64
	data, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	normalized := map[string]any{}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	return &provider.RawResult{
		StatusCode: f.rawFn(&provider.RawRequest{
			Payload:      normalized,
			OmitAuth:     req.OmitAuth,
			OverrideAuth: req.OverrideAuth,
		}),
		Body: "{}",
	}, nil
}

// strictValidator 模拟标准实现：鉴权缺失/畸形 401，非法参数 400，合法 200。
func strictValidator(req *provider.RawRequest) int {
	if req.OmitAuth || req.OverrideAuth != "" {
		return 401
	}
	p := req.Payload
	if msgs, ok := p["messages"].([]any); ok {
		if len(msgs) == 0 {
			return 400
		}
		for _, m := range msgs {
			obj, isObj := m.(map[string]any)
			if !isObj {
				return 400
			}
			role, has := obj["role"].(string)
			if !has || (role != "user" && role != "assistant" && role != "system" && role != "tool") {
				return 400
			}
			if c, present := obj["content"]; present && c == nil {
				return 400
			}
		}
	}
	inRange := func(key string, lo, hi float64) bool {
		v, ok := p[key]
		if !ok {
			return true
		}
		f, isNum := v.(float64)
		if !isNum {
			// JSON 数字统一解成 float64；int 探针（如 frequency_penalty: 2）也会是 float64
			return false
		}
		return f >= lo && f <= hi
	}
	if !inRange("top_p", 0, 1) || !inRange("temperature", 0, 2) ||
		!inRange("frequency_penalty", -2, 2) || !inRange("presence_penalty", -2, 2) {
		return 400
	}
	if v, ok := p["max_tokens"]; ok {
		f, isNum := v.(float64)
		if !isNum || f <= 0 || f > 1e8 {
			return 400
		}
	}
	return 200
}

func TestRunStrictService(t *testing.T) {
	p := &fakeRawProvider{protocol: "openai", rawFn: strictValidator}
	layer := Run(t.Context(), p)
	if len(layer.Checks) == 0 {
		t.Fatal("应产出检查项")
	}
	for _, c := range layer.Checks {
		if c.Status == types.StatusSkip || c.Status == types.StatusUnsupported {
			continue
		}
		if c.Status != types.StatusPass || c.Score < 1 {
			t.Errorf("%s: status=%s score=%v detail=%s", c.Name, c.Status, c.Score, c.Detail)
		}
	}
}

func TestRunLenientServiceFails(t *testing.T) {
	// 全盘接受的服务：所有非法输入都被 200 接受，边界检查应大量 fail
	p := &fakeRawProvider{protocol: "openai", rawFn: func(req *provider.RawRequest) int {
		if req.OmitAuth || req.OverrideAuth != "" {
			return 200 // 连鉴权都不校验
		}
		return 200
	}}
	layer := Run(t.Context(), p)
	var failed int
	for _, c := range layer.Checks {
		if c.Status == types.StatusFail {
			failed++
		}
	}
	if failed < 4 {
		t.Errorf("全盘接受的服务应有多项 fail, 实际 %d 项", failed)
	}
	auth := findCheck(&layer, "auth_boundary")
	if auth == nil || auth.Status != types.StatusFail {
		t.Errorf("鉴权不校验应判 fail, got %+v", auth)
	}
}

func TestRun5xxHalfScore(t *testing.T) {
	// 用 500 拒绝非法输入：非标准但显式拒绝，得半分且不判 fail
	p := &fakeRawProvider{protocol: "openai", rawFn: func(req *provider.RawRequest) int {
		if req.OmitAuth || req.OverrideAuth != "" {
			return 401
		}
		if strictValidator(req) != 200 {
			return 500
		}
		return 200
	}}
	layer := Run(t.Context(), p)
	c := findCheck(&layer, "top_p_boundary")
	if c == nil || c.Status != types.StatusPass {
		t.Fatalf("5xx 拒绝不应判 fail, got %+v", c)
	}
	if c.Score >= 1 || c.Score <= 0 {
		t.Errorf("5xx 拒绝应得部分分, score=%v", c.Score)
	}
	if !strings.Contains(c.Detail, "5xx") {
		t.Errorf("detail 应说明 5xx 偏差, got %q", c.Detail)
	}
}

func TestRunSkipsWithoutRawCaller(t *testing.T) {
	layer := Run(t.Context(), &plainProvider{})
	if len(layer.Checks) != 1 || layer.Checks[0].Status != types.StatusSkip {
		t.Fatalf("无 RawCaller 时应整层 skip, got %+v", layer.Checks)
	}
}

func TestProtocolSkips(t *testing.T) {
	// anthropic 无 frequency/presence_penalty，相应检查应 skip
	p := &fakeRawProvider{protocol: "anthropic", rawFn: strictValidator}
	layer := Run(t.Context(), p)
	for _, name := range []string{"frequency_penalty_boundary", "presence_penalty_boundary", "max_completion_tokens_compat"} {
		c := findCheck(&layer, name)
		if c == nil || c.Status != types.StatusSkip {
			t.Errorf("anthropic 的 %s 应 skip, got %+v", name, c)
		}
	}
}

// plainProvider 未实现 RawCaller。
type plainProvider struct{}

func (p *plainProvider) Chat(context.Context, *provider.Request) (*provider.Result, error) {
	return nil, nil
}
func (p *plainProvider) Stream(context.Context, *provider.Request) (*provider.Result, error) {
	return nil, nil
}
func (p *plainProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (p *plainProvider) Model() string                            { return "m" }
func (p *plainProvider) Protocol() string                         { return "openai" }

func findCheck(l *types.LayerResult, name string) *types.CheckResult {
	for i := range l.Checks {
		if l.Checks[i].Name == name {
			return &l.Checks[i]
		}
	}
	return nil
}
