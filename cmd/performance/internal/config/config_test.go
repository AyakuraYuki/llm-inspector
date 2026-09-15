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

func TestLoad_DatasetMode(t *testing.T) {
	dir := t.TempDir()
	ds := filepath.Join(dir, "prompts.txt")
	if err := os.WriteFile(ds, []byte("q1\n\nq2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(writeTempConfig(t, `
prompt:
  mode: "dataset"
  dataset_path: "`+ds+`"
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench := cfg.ToBenchmark()
	if !bench.DatasetPrompt || len(bench.DatasetLines) != 2 {
		t.Fatalf("DatasetPrompt=%v lines=%d, want true/2", bench.DatasetPrompt, len(bench.DatasetLines))
	}
	if p := bench.BuildPrompt(); p != "q1" && p != "q2" {
		t.Errorf("BuildPrompt 返回了数据集之外的内容 %q", p)
	}

	// 缺 dataset_path 必须报错，而不是静默退回固定文本
	if _, err := Load(writeTempConfig(t, `
prompt:
  mode: "dataset"
`+minimalModels)); err == nil {
		t.Errorf("dataset 模式缺 dataset_path 应报错")
	}
	// 非 dataset 模式却配了 dataset_path：拼写/理解错误，应报错
	if _, err := Load(writeTempConfig(t, `
prompt:
  mode: "text"
  dataset_path: "`+ds+`"
`+minimalModels)); err == nil {
		t.Errorf("text 模式配 dataset_path 应报错")
	}
}

func TestLoad_PromptTokensRange(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, `
prompt:
  mode: "dynamic"
  tokens_min: 500
  tokens_max: 1500
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench := cfg.ToBenchmark()
	if bench.PromptTokensMin != 500 || bench.PromptTokensMax != 1500 {
		t.Errorf("区间 = [%d, %d], want [500, 1500]", bench.PromptTokensMin, bench.PromptTokensMax)
	}

	bad := []string{
		"prompt:\n  mode: \"dynamic\"\n  tokens_min: 1500\n  tokens_max: 500\n", // min > max
		"prompt:\n  mode: \"dynamic\"\n  tokens_min: 500\n",                     // 只配一半
		"prompt:\n  mode: \"text\"\n  tokens_min: 500\n  tokens_max: 1500\n",    // 非 dynamic
		"prompt:\n  mode: \"dynamic\"\n  tokens_min: -1\n  tokens_max: 10\n",    // 负数
	}
	for _, b := range bad {
		if _, err := Load(writeTempConfig(t, b+minimalModels)); err == nil {
			t.Errorf("非法区间配置应报错：\n%s", b)
		}
	}
}

func TestLoad_TokenizerBlock(t *testing.T) {
	// 不存在的目录：加载失败必须在配置阶段报出
	if _, err := Load(writeTempConfig(t, `
tokenizer:
  path: "`+filepath.Join(t.TempDir(), "nope")+`"
`+minimalModels)); err == nil {
		t.Fatalf("不可加载的 tokenizer.path 应报错")
	}
	// 只配阈值不配路径：无意义，应报错
	if _, err := Load(writeTempConfig(t, `
tokenizer:
  usage_drift_pct: 5
`+minimalModels)); err == nil {
		t.Fatalf("tokenizer.usage_drift_pct 无 path 应报错")
	}

	dir := filepath.Join("..", "..", "..", "..", "configs", "tokenizers", "kimi-k3")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("词表目录不存在，跳过：%v", err)
	}
	cfg, err := Load(writeTempConfig(t, `
prompt:
  mode: "dynamic"
  tokens: 300
tokenizer:
  path: "`+dir+`"
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench := cfg.ToBenchmark()
	if bench.TokenizerPath != dir {
		t.Errorf("TokenizerPath = %q, want %q", bench.TokenizerPath, dir)
	}
	if bench.TokenizerFingerprint == "" {
		t.Errorf("TokenizerFingerprint 应随配置生成")
	}
	if bench.UsageDriftPct != 10 {
		t.Errorf("UsageDriftPct 默认应为 10，got %v", bench.UsageDriftPct)
	}
	if err := bench.ValidateTokenizer(); err != nil {
		t.Errorf("ValidateTokenizer: %v", err)
	}
	// 显式 0 关闭对拍
	cfg, err = Load(writeTempConfig(t, `
tokenizer:
  path: "`+dir+`"
  usage_drift_pct: 0
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ToBenchmark().UsageDriftPct; got != 0 {
		t.Errorf("显式 usage_drift_pct: 0 应保留为 0，got %v", got)
	}
}

func TestLoad_RequestsPerLevel(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, `
concurrency: [1, 2, 3]
requests_per_level: [100]
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench := cfg.ToBenchmark()
	for i := range 3 {
		if got := bench.LevelRequestLimit(i); got != 100 {
			t.Errorf("单项配置应作用于全部档位，档位 %d = %d", i, got)
		}
	}

	cfg, err = Load(writeTempConfig(t, `
concurrency: [1, 2, 3]
requests_per_level: [10, 20, 30]
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench = cfg.ToBenchmark()
	if bench.LevelRequestLimit(2) != 30 || bench.LevelRequestLimit(0) != 10 {
		t.Errorf("逐档配置未一一对应：%v", bench.RequestsPerLevel)
	}
	if bench.LevelRequestLimit(7) != 0 {
		t.Errorf("越界档位应返回 0")
	}

	bad := []string{
		"concurrency: [1, 2, 3]\nrequests_per_level: [10, 20]\n", // 长度不匹配
		"concurrency: [1, 2]\nrequests_per_level: [10, 0]\n",     // 非正
	}
	for _, b := range bad {
		if _, err := Load(writeTempConfig(t, b+minimalModels)); err == nil {
			t.Errorf("非法 requests_per_level 应报错：\n%s", b)
		}
	}
	// 未配置：纯时长制
	cfg, _ = Load(writeTempConfig(t, minimalModels))
	if cfg.ToBenchmark().LevelRequestLimit(0) != 0 {
		t.Errorf("未配置 requests_per_level 时应为 0")
	}
}

func TestLoad_OpenLoopUnboundedAndWarmupPerLevel(t *testing.T) {
	if _, err := Load(writeTempConfig(t, `
open_loop_unbounded: true
`+minimalModels)); err == nil {
		t.Errorf("closed 模式下 open_loop_unbounded 应报错")
	}
	cfg, err := Load(writeTempConfig(t, `
concurrency: [10]
load_mode: "open"
request_rate: [5]
open_loop_unbounded: true
warmup_per_level: true
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bench := cfg.ToBenchmark()
	if !bench.OpenLoopUnbounded {
		t.Errorf("OpenLoopUnbounded 应为 true")
	}
	if !bench.WarmupPerLevel {
		t.Errorf("warmup 默认开启时 WarmupPerLevel 应为 true")
	}
	// warmup 显式关闭时 warmup_per_level 无效
	cfg, err = Load(writeTempConfig(t, `
warmup: false
warmup_per_level: true
`+minimalModels))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ToBenchmark().WarmupPerLevel {
		t.Errorf("warmup=false 时 WarmupPerLevel 应为 false")
	}
}
