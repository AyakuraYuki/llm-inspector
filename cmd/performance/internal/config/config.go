// Package config 负责从 YAML 配置文件加载压测运行参数，并转换为
// types.BenchmarkConfig。配置文件是唯一的参数来源，命令行不再提供业务开关。
package config

import (
	"bytes"
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

	defaultMaxErrorRate = 0.5
	defaultMinSamples   = 20

	defaultPollInterval = time.Second
	defaultAgentTimeout = 10 * time.Second
)

var defaultConcurrency = []int{10, 20, 30, 40, 50, 75, 100, 120, 150}

// Config 是 YAML 配置文件的完整结构。
type Config struct {
	BaseURL        string              `yaml:"base_url"`
	Duration       time.Duration       `yaml:"duration"`
	Concurrency    []int               `yaml:"concurrency"`
	Prompt         PromptConfig        `yaml:"prompt"`
	ImagePrompt    string              `yaml:"image_prompt"`
	Warmup         *bool               `yaml:"warmup"` // 指针以区分「显式 false」与「未配置」
	WarmupDuration time.Duration       `yaml:"warmup_duration"`
	Cooldown       *time.Duration      `yaml:"cooldown"` // 指针以区分「显式 0s（档位间不等待）」与「未配置」
	Output         string              `yaml:"output"`
	NoExcel        bool                `yaml:"no_excel"`
	NoTUI          bool                `yaml:"no_tui"`
	Models         []ModelConfig       `yaml:"models"`
	Tokens         []string            `yaml:"tokens"`
	TokenGroups    map[string][]string `yaml:"token_groups"`
	EarlyStop      EarlyStopConfig     `yaml:"early_stop"`
	Cluster        *ClusterConfig      `yaml:"cluster"` // 仅 performance-cluster run 使用，单机版忽略
}

// ClusterConfig 描述分布式压测的 coordinator 侧参数（agent 列表与调度节奏）。
// 单机 performance 加载时该段合法但不使用。
type ClusterConfig struct {
	Agents       []string      `yaml:"agents"`        // agent 守护进程地址列表（host:port），必填
	AuthToken    string        `yaml:"auth_token"`    // 可选，与 agent -token 一致时通过 X-Cluster-Token 鉴权
	PollInterval time.Duration `yaml:"poll_interval"` // 进度轮询间隔，默认 1s
	AgentTimeout time.Duration `yaml:"agent_timeout"` // 连续无响应判定失联的窗口，默认 10s
}

// EarlyStopConfig 描述基于错误率的档位早停与跳档策略，默认关闭，不影响现有配置。
type EarlyStopConfig struct {
	Enabled               bool    `yaml:"enabled"`                 // 总开关，默认 false
	MaxErrorRate          float64 `yaml:"max_error_rate"`          // 档位失败率超过该值判定为不可用，(0,1]，默认 0.5
	MinSamples            int     `yaml:"min_samples"`             // 至少凑够这么多请求才评估错误率，避免开局抖动误判，默认 20
	SkipHigherConcurrency *bool   `yaml:"skip_higher_concurrency"` // 判定不可用时是否跳过该模型剩余的更高并发档位，默认 true
}

// PromptConfig 描述文本端点的 prompt 生成方式。
type PromptConfig struct {
	Mode   PromptMode `yaml:"mode"`   // text | dynamic | codex，默认 text
	Text   string     `yaml:"text"`   // mode=text 时使用的固定文本
	Tokens int        `yaml:"tokens"` // mode=dynamic 时生成文本的目标近似 token 数
}

// ModelConfig 描述一个待测模型。
type ModelConfig struct {
	Name       string `yaml:"name"`
	Provider   string `yaml:"provider"`
	TokenGroup string `yaml:"token_group"`
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
	dec := yaml.NewDecoder(bytes.NewReader(data))
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
	if c.EarlyStop.Enabled {
		if c.EarlyStop.MaxErrorRate <= 0 {
			c.EarlyStop.MaxErrorRate = defaultMaxErrorRate
		}
		if c.EarlyStop.MinSamples <= 0 {
			c.EarlyStop.MinSamples = defaultMinSamples
		}
		if c.EarlyStop.SkipHigherConcurrency == nil {
			enabled := true
			c.EarlyStop.SkipHigherConcurrency = &enabled
		}
	}
	if c.Cluster != nil {
		if c.Cluster.PollInterval <= 0 {
			c.Cluster.PollInterval = defaultPollInterval
		}
		if c.Cluster.AgentTimeout <= 0 {
			c.Cluster.AgentTimeout = defaultAgentTimeout
		}
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

	groups := c.normalizedTokenGroups()
	for i, m := range c.Models {
		group := strings.TrimSpace(m.TokenGroup)
		if group == "" {
			group = defaultTokenGroup
		}
		if len(groups[group]) == 0 {
			if group == defaultTokenGroup {
				return fmt.Errorf("models[%d] 未指定 token_group，但 tokens 中没有有效 token", i)
			}
			return fmt.Errorf("models[%d].token_group %q 不存在或不含有效 token", i, group)
		}
	}

	if c.EarlyStop.Enabled {
		if c.EarlyStop.MaxErrorRate <= 0 || c.EarlyStop.MaxErrorRate > 1 {
			return fmt.Errorf("early_stop.max_error_rate 必须在 (0, 1] 区间内，发现非法值：%v", c.EarlyStop.MaxErrorRate)
		}
		if c.EarlyStop.MinSamples <= 0 {
			return fmt.Errorf("early_stop.min_samples 必须为正整数，发现非法值：%d", c.EarlyStop.MinSamples)
		}
	}

	if c.Cluster != nil {
		if err := c.Cluster.validate(); err != nil {
			return err
		}
	}

	return nil
}

// validate 校验 cluster 段：agents 非空、逐项非空且不重复。
func (c *ClusterConfig) validate() error {
	if len(c.Agents) == 0 {
		return fmt.Errorf("cluster.agents 为必填项，至少配置一个 agent 地址")
	}
	seen := make(map[string]struct{}, len(c.Agents))
	for i, addr := range c.Agents {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return fmt.Errorf("cluster.agents[%d] 不能为空", i)
		}
		if _, dup := seen[addr]; dup {
			return fmt.Errorf("cluster.agents[%d] 重复：%s", i, addr)
		}
		seen[addr] = struct{}{}
		c.Agents[i] = addr
	}
	return nil
}

// ToBenchmark 把校验通过的配置转换为 types.BenchmarkConfig。
func (c *Config) ToBenchmark() types.BenchmarkConfig {
	groups := c.normalizedTokenGroups()
	models := make([]types.ModelSpec, 0, len(c.Models))
	for _, m := range c.Models {
		group := strings.TrimSpace(m.TokenGroup)
		if group == "" {
			group = defaultTokenGroup
		}
		models = append(models, types.ModelSpec{
			Name:       strings.TrimSpace(m.Name),
			Provider:   types.Provider(strings.ToLower(strings.TrimSpace(m.Provider))),
			TokenGroup: group,
			Tokens:     append([]string(nil), groups[group]...),
		})
	}

	warmup := c.Warmup != nil && *c.Warmup

	return types.BenchmarkConfig{
		BaseURL:               c.BaseURL,
		Models:                models,
		Concurrency:           append([]int(nil), c.Concurrency...),
		Duration:              c.Duration,
		Prompt:                c.Prompt.Text,
		ImagePrompt:           c.ImagePrompt,
		DynamicPrompt:         c.Prompt.Mode == PromptModeDynamic,
		PromptTokens:          c.Prompt.Tokens,
		CodexPrompt:           c.Prompt.Mode == PromptModeCodex,
		Warmup:                warmup,
		WarmupDuration:        c.WarmupDuration,
		CooldownDuration:      *c.Cooldown, // applyDefaults 保证非 nil
		EarlyStopEnabled:      c.EarlyStop.Enabled,
		MaxErrorRate:          c.EarlyStop.MaxErrorRate,
		MinSamples:            c.EarlyStop.MinSamples,
		SkipHigherConcurrency: c.EarlyStop.Enabled && c.EarlyStop.SkipHigherConcurrency != nil && *c.EarlyStop.SkipHigherConcurrency,
	}
}

const defaultTokenGroup = "default"

// normalizedTokenGroups 把旧版 tokens 映射为默认分组，并裁剪各组中的空 token。
func (c *Config) normalizedTokenGroups() map[string][]string {
	groups := make(map[string][]string, len(c.TokenGroups)+1)
	for name, tokens := range c.TokenGroups {
		group := strings.TrimSpace(name)
		if group == "" {
			continue
		}
		groups[group] = cleanTokens(tokens)
	}
	groups[defaultTokenGroup] = cleanTokens(c.Tokens)
	return groups
}

func cleanTokens(tokens []string) []string {
	cleaned := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			cleaned = append(cleaned, token)
		}
	}
	return cleaned
}
