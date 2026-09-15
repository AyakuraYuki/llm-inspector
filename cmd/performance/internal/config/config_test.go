package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入临时配置失败: %v", err)
	}
	return path
}

const minimalModels = `
models:
  - name: "m"
    provider: "openai"
tokens:
  - "sk-test"
`

func TestLoad_ExampleConfigStillValid(t *testing.T) {
	// 回归验证：不加任何新字段的既有配置必须继续解析成功，且新增开关全部保持关闭态，
	// 与「不改变现有功能和使用方式」的约束一致。
	cfg, err := Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatalf("Load(config.example.yaml) 失败: %v", err)
	}
	bench := cfg.ToBenchmark()
	if bench.OpenLoop {
		t.Errorf("OpenLoop = true, want false（示例配置未设置 load_mode，应保持 closed-loop）")
	}
	if bench.SLO.Enabled() {
		t.Errorf("SLO.Enabled() = true, want false（示例配置未设置 slo）")
	}
	if bench.ShowHistogram {
		t.Errorf("ShowHistogram = true, want false（示例配置未设置 show_histogram）")
	}
	if len(bench.RequestRate) != 0 {
		t.Errorf("RequestRate = %v, want empty", bench.RequestRate)
	}
	// 示例配置里 think_time/max_output_tokens 都是注释状态，应取历史默认值
	if bench.ThinkTime != 300*time.Millisecond {
		t.Errorf("ThinkTime = %v, want 300ms（示例配置未设置 think_time）", bench.ThinkTime)
	}
	if got := bench.EffectiveMaxOutputTokens(); got != types.DefaultMaxOutputTokens {
		t.Errorf("EffectiveMaxOutputTokens() = %d, want %d", got, types.DefaultMaxOutputTokens)
	}
	if cfg.SampleOutput != "" {
		t.Errorf("SampleOutput = %q, want 空", cfg.SampleOutput)
	}
}

func TestLoad_OpenLoopRequiresRequestRate(t *testing.T) {
	path := writeTempConfig(t, "load_mode: open\nconcurrency: [10, 20]\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应在 load_mode=open 且未配置 request_rate 时报错")
	}
}

func TestLoad_OpenLoopRequestRateLengthMustMatchConcurrency(t *testing.T) {
	path := writeTempConfig(t, "load_mode: open\nconcurrency: [10, 20]\nrequest_rate: [5]\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应在 request_rate 长度与 concurrency 不一致时报错")
	}
}

func TestLoad_OpenLoopValid(t *testing.T) {
	path := writeTempConfig(t, "load_mode: open\nconcurrency: [10, 20]\nrequest_rate: [5, 10]\n"+minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	bench := cfg.ToBenchmark()
	if !bench.OpenLoop {
		t.Error("OpenLoop = false, want true")
	}
	if len(bench.RequestRate) != 2 || bench.RequestRate[0] != 5 || bench.RequestRate[1] != 10 {
		t.Errorf("RequestRate = %v, want [5 10]", bench.RequestRate)
	}
}

func TestLoad_ThinkTimeDefaultsToHistoricalValue(t *testing.T) {
	// 默认值必须保持 300ms：改动它会让新旧压测报告的吞吐数字失去可比性。
	path := writeTempConfig(t, minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if got := cfg.ToBenchmark().ThinkTime; got != 300*time.Millisecond {
		t.Errorf("ThinkTime = %v, want 300ms（未配置时保持历史行为）", got)
	}
}

func TestLoad_ThinkTimeExplicitZeroHonored(t *testing.T) {
	// 显式 0s 是「完成即发」，不能被 applyDefaults 悄悄改回 300ms——
	// 这是与 evalscope `--rate -1` 对拍的前提。
	path := writeTempConfig(t, "think_time: 0s\n"+minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if got := cfg.ToBenchmark().ThinkTime; got != 0 {
		t.Errorf("ThinkTime = %v, want 0s（显式配 0 应被保留）", got)
	}
}

func TestLoad_ThinkTimeCustomValue(t *testing.T) {
	path := writeTempConfig(t, "think_time: 1s500ms\n"+minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if got := cfg.ToBenchmark().ThinkTime; got != 1500*time.Millisecond {
		t.Errorf("ThinkTime = %v, want 1.5s", got)
	}
}

func TestLoad_NegativeThinkTimeRejected(t *testing.T) {
	path := writeTempConfig(t, "think_time: -1s\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应拒绝负数 think_time")
	}
}

func TestLoad_MaxOutputTokensDefaultAndOverride(t *testing.T) {
	path := writeTempConfig(t, minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if got := cfg.ToBenchmark().EffectiveMaxOutputTokens(); got != types.DefaultMaxOutputTokens {
		t.Errorf("EffectiveMaxOutputTokens() = %d, want %d", got, types.DefaultMaxOutputTokens)
	}

	path = writeTempConfig(t, "max_output_tokens: 512\n"+minimalModels)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if got := cfg.ToBenchmark().EffectiveMaxOutputTokens(); got != 512 {
		t.Errorf("EffectiveMaxOutputTokens() = %d, want 512", got)
	}
}

func TestLoad_NegativeMaxOutputTokensRejected(t *testing.T) {
	// 负数必须报错，不能被当成「未配置」悄悄回退到 8192。
	path := writeTempConfig(t, "max_output_tokens: -1\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应拒绝负数 max_output_tokens")
	}
}

func TestLoad_SampleOutputPropagates(t *testing.T) {
	path := writeTempConfig(t, minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if cfg.SampleOutput != "" {
		t.Errorf("SampleOutput = %q, want 空（默认不导出原始样本）", cfg.SampleOutput)
	}

	path = writeTempConfig(t, "sample_output: \"s.jsonl\"\n"+minimalModels)
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	if cfg.SampleOutput != "s.jsonl" {
		t.Errorf("SampleOutput = %q, want s.jsonl", cfg.SampleOutput)
	}
}

func TestLoad_InvalidLoadMode(t *testing.T) {
	path := writeTempConfig(t, "load_mode: bogus\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应在 load_mode 非法值时报错")
	}
}

func TestLoad_SLONegativeRejected(t *testing.T) {
	path := writeTempConfig(t, "slo:\n  ttft_ms: -1\n"+minimalModels)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() 应拒绝负数 SLO 阈值")
	}
}

func TestLoad_SLOThresholdsPropagate(t *testing.T) {
	path := writeTempConfig(t, "slo:\n  ttft_ms: 800\n  e2e_ms: 10000\n"+minimalModels)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}
	bench := cfg.ToBenchmark()
	if !bench.SLO.Enabled() {
		t.Fatal("SLO.Enabled() = false, want true")
	}
	if bench.SLO.TTFT.Milliseconds() != 800 {
		t.Errorf("SLO.TTFT = %v, want 800ms", bench.SLO.TTFT)
	}
	if bench.SLO.TPOT != 0 {
		t.Errorf("SLO.TPOT = %v, want 0（未配置的维度应保持 0，不参与判定）", bench.SLO.TPOT)
	}
}
