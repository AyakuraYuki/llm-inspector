package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// writeTempConfig 把 content 写入临时文件并返回路径。
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoad_Minimal_AppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, `
models:
  - name: gpt-5.6-sol
    provider: openai
tokens:
  - sk-abcdef0123456789
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, defaultBaseURL, cfg.BaseURL)
	assert.Equal(t, defaultDuration, cfg.Duration)
	assert.Equal(t, defaultConcurrency, cfg.Concurrency)
	assert.Equal(t, PromptModeText, cfg.Prompt.Mode)
	assert.Equal(t, defaultPromptText, cfg.Prompt.Text)
	assert.Equal(t, defaultPromptTokens, cfg.Prompt.Tokens)
	assert.Equal(t, defaultImagePrompt, cfg.ImagePrompt)
	require.NotNil(t, cfg.Warmup)
	assert.True(t, *cfg.Warmup)
	assert.Equal(t, defaultWarmupDuration, cfg.WarmupDuration)
	require.NotNil(t, cfg.Cooldown)
	assert.Equal(t, defaultCooldown, *cfg.Cooldown)
}

func TestLoad_WarmupExplicitFalse(t *testing.T) {
	path := writeTempConfig(t, `
warmup: false
models:
  - name: gpt-5.6-sol
    provider: openai
tokens:
  - sk-abcdef0123456789
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Warmup)
	assert.False(t, *cfg.Warmup)
	assert.False(t, cfg.ToBenchmark().Warmup)
}

func TestLoad_CooldownExplicitZero(t *testing.T) {
	path := writeTempConfig(t, `
cooldown: 0s
models:
  - name: gpt-5.6-sol
    provider: openai
tokens:
  - sk-abcdef0123456789
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Cooldown)
	// 显式 0s 不应被默认值覆盖，档位间不等待
	assert.Equal(t, time.Duration(0), *cfg.Cooldown)
	assert.Equal(t, time.Duration(0), cfg.ToBenchmark().CooldownDuration)
}

func TestLoad_FullConfig_ToBenchmark(t *testing.T) {
	path := writeTempConfig(t, `
base_url: https://example.com
duration: 30s
concurrency: [100, 200]
prompt:
  mode: dynamic
  text: custom text
  tokens: 1234
image_prompt: a blue square
warmup: true
warmup_duration: 3s
cooldown: 2s
output: out.xlsx
no_excel: true
no_tui: true
models:
  - name: claude-sonnet-5
    provider: anthropic
tokens:
  - "  tok-1  "
  - ""
  - tok-2
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.True(t, cfg.NoExcel)
	assert.True(t, cfg.NoTUI)
	assert.Equal(t, "out.xlsx", cfg.Output)

	bench := cfg.ToBenchmark()
	assert.Equal(t, "https://example.com", bench.BaseURL)
	assert.Equal(t, 30*time.Second, bench.Duration)
	assert.Equal(t, []int{100, 200}, bench.Concurrency)
	assert.True(t, bench.DynamicPrompt)
	assert.False(t, bench.CodexPrompt)
	assert.Equal(t, 1234, bench.PromptTokens)
	assert.Equal(t, "a blue square", bench.ImagePrompt)
	assert.Equal(t, 3*time.Second, bench.WarmupDuration)
	assert.Equal(t, 2*time.Second, bench.CooldownDuration)
	// token 前后空白被裁剪、空 token 被丢弃
	assert.Equal(t, []string{"tok-1", "tok-2"}, bench.Tokens)
	require.Len(t, bench.Models, 1)
	assert.Equal(t, types.Provider("anthropic"), bench.Models[0].Provider)
}

func TestLoad_CodexMode(t *testing.T) {
	path := writeTempConfig(t, `
prompt:
  mode: codex
models:
  - name: gpt-5.6-sol
    provider: openai
tokens:
  - sk-1
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	bench := cfg.ToBenchmark()
	assert.True(t, bench.CodexPrompt)
	assert.False(t, bench.DynamicPrompt)
}

func TestLoad_Errors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "unknown field",
			content: `
base_urls: oops
models:
  - name: m
    provider: openai
tokens: [t]
`,
			wantErr: "解析配置文件失败",
		},
		{
			name: "invalid prompt mode",
			content: `
prompt:
  mode: bogus
models:
  - name: m
    provider: openai
tokens: [t]
`,
			wantErr: "prompt.mode 非法",
		},
		{
			name: "unknown provider",
			content: `
models:
  - name: m
    provider: notreal
tokens: [t]
`,
			wantErr: "未知 provider",
		},
		{
			name: "empty model name",
			content: `
models:
  - name: "  "
    provider: openai
tokens: [t]
`,
			wantErr: "name 不能为空",
		},
		{
			name: "no models",
			content: `
tokens: [t]
`,
			wantErr: "models 为必填项",
		},
		{
			name: "no valid tokens",
			content: `
models:
  - name: m
    provider: openai
tokens:
  - "   "
`,
			wantErr: "tokens 为必填项",
		},
		{
			name: "non-positive concurrency",
			content: `
concurrency: [100, 0]
models:
  - name: m
    provider: openai
tokens: [t]
`,
			wantErr: "concurrency 必须为正整数",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempConfig(t, tc.content)
			_, err := Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "读取配置文件失败")
}
