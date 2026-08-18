// Package config 负责加载与校验 YAML 评测配置，并填充默认值。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是评测配置的根结构。
type Config struct {
	Layers     LayersConfig     `yaml:"layers"`
	Judge      *TargetConfig    `yaml:"judge"`
	Tool       string           `yaml:"-"`
	Output     OutputConfig     `yaml:"output"`
	Target     TargetConfig     `yaml:"target"`
	Thresholds ThresholdsConfig `yaml:"thresholds"`
}

// Load 从文件加载配置并填充默认值。
// 配置中的环境变量引用（见 envexpand.go）在解析为节点树后、解码为结构体前展开。
func Load(path string, programName string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}

	var root yaml.Node
	if err = yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if isEmptyDocument(&root) { // 空文档、纯注释或裸 null
		return nil, fmt.Errorf("配置文件为空: %s", path)
	}

	if err = expandEnvInNode(&root, path); err != nil {
		return nil, fmt.Errorf("填充环境变量失败:\n%w", err)
	}

	conf := new(Config)
	if err = root.Decode(conf); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	conf.defaults()

	if err = conf.validate(); err != nil {
		return nil, err
	}

	conf.Tool = programName
	return conf, nil
}

func (conf *Config) defaults() {
	if conf.Layers.Capability.Concurrency <= 0 {
		conf.Layers.Capability.Concurrency = 4
	}
	stability := &conf.Layers.Stability
	if stability.Samples <= 0 {
		stability.Samples = 5
	}
	if stability.SoakRequests <= 0 {
		stability.SoakRequests = 50
	}
	if stability.Temperature == nil {
		stability.Temperature = new(1.0)
	}
	performance := &conf.Layers.Performance
	if performance.Runs <= 0 {
		performance.Runs = 20
	}
	if len(performance.Concurrency) == 0 {
		performance.Concurrency = []int{1, 4, 16}
	}
	if performance.MaxProbeTokens <= 0 {
		performance.MaxProbeTokens = 32768
	}
	if performance.SLO.TTFTP99MS <= 0 {
		performance.SLO.TTFTP99MS = 2000
	}
	if performance.SLO.MinTokensPerSec <= 0 {
		performance.SLO.MinTokensPerSec = 10
	}
	if performance.SLO.MaxErrorRate <= 0 {
		performance.SLO.MaxErrorRate = 0.01
	}
	if conf.Thresholds.MinLayerScore <= 0 {
		conf.Thresholds.MinLayerScore = 0.8
	}
	if conf.Output.Dir == "" {
		conf.Output.Dir = "./reports"
	}
	if len(conf.Output.Formats) == 0 {
		conf.Output.Formats = []string{"json", "markdown"}
	}
}

func (conf *Config) validate() error {
	if conf.Target.BaseURL == "" {
		return fmt.Errorf("缺少 target.base_url")
	}
	if conf.Target.Model == "" {
		return fmt.Errorf("缺少 target.model")
	}
	switch conf.Target.ProtocolNormalized() {
	case "openai", "anthropic", "gemini":
	default:
		return fmt.Errorf("未知 target.protocol %q（支持 openai/anthropic/gemini）", conf.Target.Protocol)
	}
	if _, err := conf.Target.TimeoutDuration(); err != nil {
		return err
	}
	if conf.Judge != nil {
		if conf.Judge.BaseURL == "" || conf.Judge.Model == "" {
			return fmt.Errorf("judge 需要 base_url 与 model")
		}
		switch conf.Judge.ProtocolNormalized() {
		case "openai", "anthropic", "gemini":
		default:
			return fmt.Errorf("未知 judge.protocol %q", conf.Judge.Protocol)
		}
		if _, err := conf.Judge.TimeoutDuration(); err != nil {
			return err
		}
	}
	for _, c := range conf.Layers.Performance.Concurrency {
		if c <= 0 {
			return fmt.Errorf("performance.concurrency 必须为正整数")
		}
	}
	return nil
}

// TargetConfig 描述一个模型服务端点。
type TargetConfig struct {
	BaseURL     string           `yaml:"base_url"`
	APIKey      string           `yaml:"api_key"`
	Model       string           `yaml:"model"`
	Protocol    string           `yaml:"protocol"`    // openai（默认）/ anthropic / gemini
	Timeout     string           `yaml:"timeout"`     // 如 "60s"，默认 60s
	Constraints ModelConstraints `yaml:"constraints"` // 模型特定的参数约束
}

// ProtocolNormalized 返回规范化后的协议名（缺省 openai）。
func (t *TargetConfig) ProtocolNormalized() string {
	if t.Protocol == "" {
		return "openai"
	}
	return strings.ToLower(strings.TrimSpace(t.Protocol))
}

// WithAPIKey 返回替换 API key 后的副本。
func (t *TargetConfig) WithAPIKey(key string) TargetConfig {
	return TargetConfig{
		BaseURL:     t.BaseURL,
		APIKey:      key,
		Model:       t.Model,
		Protocol:    t.Protocol,
		Timeout:     t.Timeout,
		Constraints: t.Constraints.Clone(),
	}
}

// TimeoutDuration 解析 Timeout 字符串。
func (t *TargetConfig) TimeoutDuration() (time.Duration, error) {
	if t.Timeout == "" {
		return 60 * time.Second, nil
	}
	d, err := time.ParseDuration(t.Timeout)
	if err != nil {
		return 0, fmt.Errorf("无效的 timeout %q: %w", t.Timeout, err)
	}
	return d, nil
}

// ModelConstraints 定义模型的参数约束，用于覆盖默认测试行为。
type ModelConstraints struct {
	SpecifiedTemperature *float64 `yaml:"specified_temperature"` // 可选的指定 temperature 值（未配置则跳过指定温度检查）

	// ThinkingEnableParams / ThinkingDisableParams 是开启/关闭思考的厂商参数
	// （如 GLM 的 {thinking: {type: enabled}}），原样合并进请求体
	// （openai/anthropic 顶层，gemini 的 generationConfig）。
	// 两者都未配置时跳过 thinking 控制检查。
	ThinkingEnableParams  *ThinkingParams `yaml:"thinking_enable_params"`
	ThinkingDisableParams *ThinkingParams `yaml:"thinking_disable_params"`

	// ReasoningEfforts 模型声称支持的 reasoning_effort 值（仅 openai 协议探测）。
	ReasoningEfforts []string `yaml:"reasoning_efforts"`

	// DefaultMaxTokens 官方标称的 max_tokens 默认值（如 GLM-5.2 为 32768）。
	// 配置后 L2 会做默认值探测：不传 max_tokens 观察输出是否受该默认值约束。
	DefaultMaxTokens            int  `yaml:"default_max_tokens"`
	DisableTemperatureZeroCheck bool `yaml:"disable_temperature_zero_check"` // 禁用 temperature=0 一致性检查
}

func (c ModelConstraints) Clone() ModelConstraints {
	constraints := ModelConstraints{
		DisableTemperatureZeroCheck: c.DisableTemperatureZeroCheck,
		DefaultMaxTokens:            c.DefaultMaxTokens,
	}
	if c.SpecifiedTemperature != nil {
		constraints.SpecifiedTemperature = new(*c.SpecifiedTemperature)
	}
	if c.ThinkingEnableParams != nil {
		constraints.ThinkingEnableParams = &ThinkingParams{
			Thinking: c.ThinkingEnableParams.Thinking,
		}
	}
	if c.ThinkingDisableParams != nil {
		constraints.ThinkingDisableParams = &ThinkingParams{
			Thinking: c.ThinkingDisableParams.Thinking,
		}
	}
	if len(c.ReasoningEfforts) > 0 {
		constraints.ReasoningEfforts = make([]string, len(c.ReasoningEfforts))
		copy(constraints.ReasoningEfforts, c.ReasoningEfforts)
	}
	return constraints
}

type ThinkingType string

const (
	ThinkingEnabled  = "enabled"
	ThinkingDisabled = "disabled"
)

type Thinking struct {
	Type ThinkingType `yaml:"type"`
}

type ThinkingParams struct {
	Thinking Thinking `yaml:"thinking"`
}

func (p *ThinkingParams) ToMap() map[string]any {
	if p != nil {
		return map[string]any{
			"thinking": map[string]any{
				"type": string(p.Thinking.Type),
			},
		}
	}
	return nil
}

// LayersConfig 各层配置。Enabled 为 nil 时默认启用。
type LayersConfig struct {
	Stability    StabilityConfig    `yaml:"stability"`
	Availability AvailabilityConfig `yaml:"availability"`
	Protocol     ProtocolConfig     `yaml:"protocol"`
	Boundary     BoundaryConfig     `yaml:"boundary"`
	Capability   CapabilityConfig   `yaml:"capability"`
	Performance  PerformanceConfig  `yaml:"performance"`
}

type AvailabilityConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type ProtocolConfig struct {
	Enabled *bool `yaml:"enabled"`
}

// BoundaryConfig L6 参数边界与健壮性配置。
type BoundaryConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type CapabilityConfig struct {
	Enabled     *bool  `yaml:"enabled"`
	Dataset     string `yaml:"dataset"`     // 数据集 YAML 路径，为空用内建题库
	Concurrency int    `yaml:"concurrency"` // 题目并发度，默认 4
}

type StabilityConfig struct {
	Enabled      *bool    `yaml:"enabled"`
	Temperature  *float64 `yaml:"temperature"`   // 采样温度，默认 1.0
	Samples      int      `yaml:"samples"`       // 自一致性采样次数，默认 5
	SoakRequests int      `yaml:"soak_requests"` // 浸测请求数，默认 50
}

type PerformanceConfig struct {
	Enabled        *bool     `yaml:"enabled"`
	Concurrency    []int     `yaml:"concurrency"` // 并发梯度，默认 [1,4,16]
	SLO            SLOConfig `yaml:"slo"`
	Runs           int       `yaml:"runs"`             // 延迟测量次数，默认 20
	MaxProbeTokens int       `yaml:"max_probe_tokens"` // 上下文探测上限，默认 32768
}

// SLOConfig 性能评分的服务水平目标。
type SLOConfig struct {
	TTFTP99MS       float64 `yaml:"ttft_p99_ms"`        // TTFT P99 上限，默认 2000
	MinTokensPerSec float64 `yaml:"min_tokens_per_sec"` // 单流吞吐下限，默认 10
	MaxErrorRate    float64 `yaml:"max_error_rate"`     // 并发下错误率上限，默认 0.01
}

type ThresholdsConfig struct {
	MinLayerScore float64 `yaml:"min_layer_score"` // 层通过线，默认 0.8
	FailFast      bool    `yaml:"fail_fast"`       // 层不达标即中止（L1 门控不受此开关影响）
}

type OutputConfig struct {
	Dir     string   `yaml:"dir"`     // 报告输出目录，默认 ./reports
	Formats []string `yaml:"formats"` // json / markdown，默认两者
}
