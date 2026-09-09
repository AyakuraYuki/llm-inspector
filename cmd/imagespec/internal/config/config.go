// Package config 负责从 YAML 配置文件加载 imagespec 的运行参数。
// 配置文件是唯一的参数来源，命令行只保留 -config 与 -list。
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultBaseURL     = "https://api.openai.com"
	defaultModel       = "gpt-image-2"
	defaultPrompt      = "A cute fluffy kitten playing with a ball of yarn, soft lighting, adorable, high detail."
	defaultConcurrency = 2
	defaultTimeout     = 5 * time.Minute
)

// Config 是 YAML 配置文件的完整结构。
type Config struct {
	BaseURL      string        `yaml:"base_url"`
	APIKey       string        `yaml:"api_key"` // 支持 ${ENV} 展开；留空时读取环境变量 OPENAI_API_KEY
	Model        string        `yaml:"model"`
	Prompt       string        `yaml:"prompt"`
	Concurrency  int           `yaml:"concurrency"`
	Timeout      time.Duration `yaml:"timeout"`
	IncludeProbe bool          `yaml:"include_probe"`
	Only         []string      `yaml:"only"`
	Skip         []string      `yaml:"skip"`
	SaveImages   string        `yaml:"save_images"`
	Output       string        `yaml:"output"`
}

// Load 从 path 读取 YAML 配置，填充默认值并校验。
// api_key 允许为空（-list 模式不需要），由 main 在真正发起请求前检查。
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

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = defaultBaseURL
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.APIKey = strings.TrimSpace(os.ExpandEnv(c.APIKey))
	if c.APIKey == "" {
		c.APIKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = defaultModel
	}
	if strings.TrimSpace(c.Prompt) == "" {
		c.Prompt = defaultPrompt
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultConcurrency
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
}

func (c *Config) validate() error {
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return fmt.Errorf("base_url 必须以 http:// 或 https:// 开头：%q", c.BaseURL)
	}
	return nil
}
