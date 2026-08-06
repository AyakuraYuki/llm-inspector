// Package config 负责从 YAML 配置文件加载压测运行参数，并转换为
// types.BenchmarkConfig。配置文件是唯一的参数来源，命令行不再提供业务开关。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// PromptMode 定义文本端点的 prompt 生成方式，三者互斥。
type PromptMode string

const (
	// PromptModeText 使用固定的 prompt.text 文本。
	PromptModeText PromptMode = "text"
	// PromptModeDynamic 每次请求现场拼装目标长度的随机长文本，用于长上下文压测。
	PromptModeDynamic PromptMode = "dynamic"
	// PromptModeCodex 使用类 Codex 系统提示词加随机简短提问，模拟高相似度请求场景。
	PromptModeCodex PromptMode = "codex"
)

// 各字段的默认值，与旧命令行 flag 的默认值保持一致。
const (
	defaultBaseURL        = "https://api.openai.com"
	defaultDuration       = 60 * time.Second
	defaultWarmupDuration = 10 * time.Second
	defaultCooldown       = 5 * time.Second
	defaultPromptTokens   = 2000

	defaultPromptText  = "Explain in plain English what API latency and throughput mean for a developer integrating LLM APIs. Write about 120 words. Do not use bullet points."
	defaultImagePrompt = "A single red circle on white background, minimal flat design."
)

var defaultConcurrency = []int{10, 20, 30, 40, 50, 75, 100, 120, 150}

// Config 是 YAML 配置文件的完整结构。
type Config struct {
	BaseURL        string         `yaml:"base_url"`
	Duration       time.Duration  `yaml:"duration"`
	Concurrency    []int          `yaml:"concurrency"`
	Prompt         PromptConfig   `yaml:"prompt"`
	ImagePrompt    string         `yaml:"image_prompt"`
	Warmup         *bool          `yaml:"warmup"` // 指针以区分「显式 false」与「未配置」
	WarmupDuration time.Duration  `yaml:"warmup_duration"`
	Cooldown       *time.Duration `yaml:"cooldown"` // 指针以区分「显式 0s（档位间不等待）」与「未配置」
	Output         string         `yaml:"output"`
	NoExcel        bool           `yaml:"no_excel"`
	NoTUI          bool           `yaml:"no_tui"`
	Models         []ModelConfig  `yaml:"models"`
	Tokens         []string       `yaml:"tokens"`
}

// PromptConfig 描述文本端点的 prompt 生成方式。
type PromptConfig struct {
	Mode   PromptMode `yaml:"mode"`   // text | dynamic | codex，默认 text
	Text   string     `yaml:"text"`   // mode=text 时使用的固定文本
	Tokens int        `yaml:"tokens"` // mode=dynamic 时生成文本的目标近似 token 数
}

// ModelConfig 描述一个待测模型。
type ModelConfig struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
}

// Load 从 path 读取 YAML 配置，填充默认值并做完整校验。
// NoTUI/NoExcel/Output 等输出偏好保留在 Config 上，压测参数通过
// ToBenchmark 转换为 types.BenchmarkConfig。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // 拒绝未知字段，及早暴露拼写错误
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults 为未配置的字段填充默认值。
func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = defaultBaseURL
	}
	if c.Duration <= 0 {
		c.Duration = defaultDuration
	}
	if len(c.Concurrency) == 0 {
		c.Concurrency = append([]int(nil), defaultConcurrency...)
	}
	if c.Prompt.Mode == "" {
		c.Prompt.Mode = PromptModeText
	}
	if strings.TrimSpace(c.Prompt.Text) == "" {
		c.Prompt.Text = defaultPromptText
	}
	if c.Prompt.Tokens <= 0 {
		c.Prompt.Tokens = defaultPromptTokens
	}
	if strings.TrimSpace(c.ImagePrompt) == "" {
		c.ImagePrompt = defaultImagePrompt
	}
	if c.Warmup == nil {
		enabled := true
		c.Warmup = &enabled
	}
	if c.WarmupDuration <= 0 {
		c.WarmupDuration = defaultWarmupDuration
	}
	if c.Cooldown == nil {
		d := defaultCooldown
		c.Cooldown = &d
	}
}

// validate 校验必填项与取值合法性。默认值已在 applyDefaults 中填充，
// 此处只需检查用户可能填错的内容。
func (c *Config) validate() error {
	switch c.Prompt.Mode {
	case PromptModeText, PromptModeDynamic, PromptModeCodex:
	default:
		return fmt.Errorf("prompt.mode 非法：%q（合法值：%s、%s、%s）",
			c.Prompt.Mode, PromptModeText, PromptModeDynamic, PromptModeCodex)
	}

	for _, v := range c.Concurrency {
		if v <= 0 {
			return fmt.Errorf("concurrency 必须为正整数，发现非法值：%d", v)
		}
	}

	if len(c.Models) == 0 {
		return fmt.Errorf("models 为必填项，至少配置一个模型")
	}
	for i, m := range c.Models {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("models[%d].name 不能为空", i)
		}
		p := types.Provider(strings.ToLower(strings.TrimSpace(m.Provider)))
		if !runner.IsSupportedProvider(p) {
			return fmt.Errorf("models[%d]：未知 provider %q（合法值：%s）", i, m.Provider, runner.RegisteredProviders())
		}
	}

	var validTokens int
	for _, t := range c.Tokens {
		if strings.TrimSpace(t) != "" {
			validTokens++
		}
	}
	if validTokens == 0 {
		return fmt.Errorf("tokens 为必填项，至少配置一个有效 token")
	}

	return nil
}

// ToBenchmark 把校验通过的配置转换为 types.BenchmarkConfig。
func (c *Config) ToBenchmark() types.BenchmarkConfig {
	tokens := make([]string, 0, len(c.Tokens))
	for _, t := range c.Tokens {
		if s := strings.TrimSpace(t); s != "" {
			tokens = append(tokens, s)
		}
	}

	models := make([]types.ModelSpec, 0, len(c.Models))
	for _, m := range c.Models {
		models = append(models, types.ModelSpec{
			Name:     strings.TrimSpace(m.Name),
			Provider: types.Provider(strings.ToLower(strings.TrimSpace(m.Provider))),
		})
	}

	warmup := c.Warmup != nil && *c.Warmup

	return types.BenchmarkConfig{
		BaseURL:          c.BaseURL,
		Tokens:           tokens,
		Models:           models,
		Concurrency:      append([]int(nil), c.Concurrency...),
		Duration:         c.Duration,
		Prompt:           c.Prompt.Text,
		ImagePrompt:      c.ImagePrompt,
		DynamicPrompt:    c.Prompt.Mode == PromptModeDynamic,
		PromptTokens:     c.Prompt.Tokens,
		CodexPrompt:      c.Prompt.Mode == PromptModeCodex,
		Warmup:           warmup,
		WarmupDuration:   c.WarmupDuration,
		CooldownDuration: *c.Cooldown, // applyDefaults 保证非 nil
	}
}
