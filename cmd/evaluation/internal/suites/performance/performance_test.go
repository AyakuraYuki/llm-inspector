package performance

import (
	"context"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// stubProvider 按预设结果应答 Stream 调用。
type stubProvider struct {
	results []*params.Result
}

func (s *stubProvider) Chat(context.Context, *params.Request) (*params.Result, error) {
	return s.results[0], nil
}

func (s *stubProvider) Stream(_ context.Context, _ *params.Request) (*params.Result, error) {
	resp := s.results[0]
	s.results = s.results[1:]
	return resp, nil
}

func (s *stubProvider) Models(context.Context) ([]string, error) { return nil, nil }
func (s *stubProvider) Model() string                            { return "stub-model" }
func (s *stubProvider) Protocol() string                         { return "openai" }

func testConfig(runs int) config.PerformanceConfig {
	return config.PerformanceConfig{
		Runs: runs,
		SLO: config.SLOConfig{
			TTFTP99MS:       2000,
			MinTokensPerSec: 10,
		},
	}
}

func metricInt(t *testing.T, c types.CheckResult, key string) int {
	t.Helper()
	v, ok := c.Metrics[key].(int)
	if !ok {
		t.Fatalf("metric %q missing or not int: %v", key, c.Metrics[key])
	}
	return v
}

func metricFloat64(t *testing.T, c types.CheckResult, key string) float64 {
	t.Helper()
	v, ok := c.Metrics[key].(float64)
	if !ok {
		t.Fatalf("metric %q missing or not float64: %v", key, c.Metrics[key])
	}
	return v
}

// 正常流式：生成窗口 1s、50 tokens → 解码 TPS 50，样本入样。
func TestMeasureLatencyThroughput_NormalSample(t *testing.T) {
	p := &stubProvider{results: []*params.Result{
		{TTFTMS: 100, LatencyMS: 1100, CompletionTokens: 50},
	}}
	_, throughput := measureLatencyThroughput(context.Background(), p, testConfig(1))

	if got := metricFloat64(t, throughput, "tps_mean"); got != 50 {
		t.Errorf("tps_mean = %f, want 50", got)
	}
	if got := metricInt(t, throughput, "tps_excluded"); got != 0 {
		t.Errorf("tps_excluded = %d, want 0", got)
	}
	// E2E TPS = 50 / 1.1s ≈ 45.45，与解码口径分开记录
	if got := metricFloat64(t, throughput, "tps_e2e_mean"); got < 45 || got > 46 {
		t.Errorf("tps_e2e_mean = %f, want ≈45.45", got)
	}
}

// 伪流式（TTFT 贴着流结束，生成窗口 1ms）：100 tokens/1ms = 10 万 tok/s
// 的排空样本必须判伪剔除，不得污染 tps_mean。
func TestMeasureLatencyThroughput_BurstExcluded(t *testing.T) {
	p := &stubProvider{results: []*params.Result{
		{TTFTMS: 1000, LatencyMS: 1001, CompletionTokens: 100},
	}}
	_, throughput := measureLatencyThroughput(context.Background(), p, testConfig(1))

	if got := metricInt(t, throughput, "tps_excluded"); got != 1 {
		t.Errorf("tps_excluded = %d, want 1（排空样本应判伪）", got)
	}
	if throughput.Status != types.StatusFail {
		t.Errorf("Status = %v, want Fail（唯一样本被剔除后无有效吞吐样本）", throughput.Status)
	}
	// E2E 口径不受影响：100 tokens / 1.001s ≈ 99.9
	if got := metricFloat64(t, throughput, "tps_e2e_mean"); got < 99 || got > 101 {
		t.Errorf("tps_e2e_mean = %f, want ≈99.9（E2E 口径不受剔除影响）", got)
	}
}

// 超单流物理上限（窗口本身正常）：5000 tok/s > 4096 天花板，判伪。
func TestMeasureLatencyThroughput_PhysicalCeilingExcluded(t *testing.T) {
	p := &stubProvider{results: []*params.Result{
		{TTFTMS: 100, LatencyMS: 2100, CompletionTokens: 10_000}, // 2s 窗口，5000 tok/s
	}}
	_, throughput := measureLatencyThroughput(context.Background(), p, testConfig(1))

	if got := metricInt(t, throughput, "tps_excluded"); got != 1 {
		t.Errorf("tps_excluded = %d, want 1（超物理上限样本应判伪）", got)
	}
}

// usage 缺失时按文本构成估算（正文 + 思考内容）。
func TestEstimateTokens_Fallback(t *testing.T) {
	resp := &params.Result{Content: "你好世界", ReasoningContent: "思考"} // 共 6 CJK 字符
	if got := estimateTokens(resp); got != 4 {
		t.Errorf("estimateTokens = %d, want 4（6 CJK / 1.5）", got)
	}

	resp = &params.Result{CompletionTokens: 42, Content: "ignored"}
	if got := estimateTokens(resp); got != 42 {
		t.Errorf("estimateTokens = %d, want 42（usage 优先）", got)
	}
}
