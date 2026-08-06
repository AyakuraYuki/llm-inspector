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
	Target     TargetConfig     `yaml:"target"`
	Judge      *TargetConfig    `yaml:"judge"`
	Layers     LayersConfig     `yaml:"layers"`
	Thresholds ThresholdsConfig `yaml:"thresholds"`
	Output     OutputConfig     `yaml:"output"`
	Tool       string           `yaml:"-"`
}

// TargetConfig 描述一个模型服务端点。
type TargetConfig struct {
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
	Protocol string `yaml:"protocol"` // openai（默认）/ anthropic / gemini
	Timeout  string `yaml:"timeout"`  // 如 "60s"，默认 60s
}

// ProtocolNormalized 返回规范化后的协议名（缺省 openai）。
func (t *TargetConfig) ProtocolNormalized() string {
	if t.Protocol == "" {
		return "openai"
	}
	return strings.ToLower(strings.TrimSpace(t.Protocol))
}

// WithAPIKey 返回替换 API key 后的副本。
func (t TargetConfig) WithAPIKey(key string) TargetConfig {
	t.APIKey = key
	return t
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

// LayersConfig 各层配置。Enabled 为 nil 时默认启用。
type LayersConfig struct {
	Availability AvailabilityConfig `yaml:"availability"`
	Protocol     ProtocolConfig     `yaml:"protocol"`
	Capability   CapabilityConfig   `yaml:"capability"`
	Stability    StabilityConfig    `yaml:"stability"`
	Performance  PerformanceConfig  `yaml:"performance"`
}

type AvailabilityConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type ProtocolConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type CapabilityConfig struct {
	Enabled     *bool  `yaml:"enabled"`
	Dataset     string `yaml:"dataset"`     // 数据集 YAML 路径，为空用内建题库
	Concurrency int    `yaml:"concurrency"` // 题目并发度，默认 4
}

type StabilityConfig struct {
	Enabled      *bool    `yaml:"enabled"`
	Samples      int      `yaml:"samples"`       // 自一致性采样次数，默认 5
	SoakRequests int      `yaml:"soak_requests"` // 浸测请求数，默认 50
	Temperature  *float64 `yaml:"temperature"`   // 采样温度，默认 1.0
}

type PerformanceConfig struct {
	Enabled        *bool     `yaml:"enabled"`
	Runs           int       `yaml:"runs"`             // 延迟测量次数，默认 20
	Concurrency    []int     `yaml:"concurrency"`      // 并发梯度，默认 [1,4,16]
	MaxProbeTokens int       `yaml:"max_probe_tokens"` // 上下文探测上限，默认 32768
	SLO            SLOConfig `yaml:"slo"`
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

// Load 从文件加载配置并填充默认值。
func Load(path string, programName string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.Tool = programName
	return &cfg, nil
}

func (cfg *Config) defaults() {
	if cfg.Layers.Capability.Concurrency <= 0 {
		cfg.Layers.Capability.Concurrency = 4
	}
	st := &cfg.Layers.Stability
	if st.Samples <= 0 {
		st.Samples = 5
	}
	if st.SoakRequests <= 0 {
		st.SoakRequests = 50
	}
	if st.Temperature == nil {
		v := 1.0
		st.Temperature = &v
	}
	pf := &cfg.Layers.Performance
	if pf.Runs <= 0 {
		pf.Runs = 20
	}
	if len(pf.Concurrency) == 0 {
		pf.Concurrency = []int{1, 4, 16}
	}
	if pf.MaxProbeTokens <= 0 {
		pf.MaxProbeTokens = 32768
	}
	if pf.SLO.TTFTP99MS <= 0 {
		pf.SLO.TTFTP99MS = 2000
	}
	if pf.SLO.MinTokensPerSec <= 0 {
		pf.SLO.MinTokensPerSec = 10
	}
	if pf.SLO.MaxErrorRate <= 0 {
		pf.SLO.MaxErrorRate = 0.01
	}
	if cfg.Thresholds.MinLayerScore <= 0 {
		cfg.Thresholds.MinLayerScore = 0.8
	}
	if cfg.Output.Dir == "" {
		cfg.Output.Dir = "./reports"
	}
	if len(cfg.Output.Formats) == 0 {
		cfg.Output.Formats = []string{"json", "markdown"}
	}
}

func (cfg *Config) validate() error {
	if cfg.Target.BaseURL == "" {
		return fmt.Errorf("缺少 target.base_url")
	}
	if cfg.Target.Model == "" {
		return fmt.Errorf("缺少 target.model")
	}
	switch cfg.Target.ProtocolNormalized() {
	case "openai", "anthropic", "gemini":
	default:
		return fmt.Errorf("未知 target.protocol %q（支持 openai/anthropic/gemini）", cfg.Target.Protocol)
	}
	if _, err := cfg.Target.TimeoutDuration(); err != nil {
		return err
	}
	if cfg.Judge != nil {
		if cfg.Judge.BaseURL == "" || cfg.Judge.Model == "" {
			return fmt.Errorf("judge 需要 base_url 与 model")
		}
		switch cfg.Judge.ProtocolNormalized() {
		case "openai", "anthropic", "gemini":
		default:
			return fmt.Errorf("未知 judge.protocol %q", cfg.Judge.Protocol)
		}
		if _, err := cfg.Judge.TimeoutDuration(); err != nil {
			return err
		}
	}
	for _, c := range cfg.Layers.Performance.Concurrency {
		if c <= 0 {
			return fmt.Errorf("performance.concurrency 必须为正整数")
		}
	}
	return nil
}

// Enabled 返回层是否启用（nil 视为启用）。
func Enabled(b *bool) bool { return b == nil || *b }
