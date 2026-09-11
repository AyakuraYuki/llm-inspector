package config

import (
	"os"
	"path/filepath"
	"testing"
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
