package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/dataset"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// Config 从 YAML 加载运行所需的配置
type Config struct {
	BaseURL         string           `yaml:"base_url"`
	APIKey          string           `yaml:"api_key"`
	Model           string           `yaml:"model"`
	MaxTokens       int              `yaml:"max_tokens"`
	MaxWorkers      int              `yaml:"max_workers"`
	Temperature     *float32         `yaml:"temperature"`
	TopP            *float32         `yaml:"top_p"`
	ReasoningEffort string           `yaml:"reasoning_effort"`
	Dataset         dataset.Config   `yaml:"dataset"`
	CustomQuestions []types.Question `yaml:"custom_questions"`
	ReportDir       string           `yaml:"report_dir"`

	datasetQuestions []types.Question
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	cfg.datasetQuestions, err = cfg.Dataset.LoadProblems()
	if err != nil {
		return nil, fmt.Errorf("无法加载数据集: %w", err)
	}
	if cfg.ReportDir == "" {
		cfg.ReportDir = "./reports"
	}
	if err = cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *Config) validate() error {
	if cfg.BaseURL == "" {
		return errors.New("缺少 base_url")
	}
	if cfg.APIKey == "" {
		return errors.New("缺少 api_key")
	}
	if cfg.Model == "" {
		return errors.New("缺少 model")
	}
	if len(cfg.datasetQuestions) == 0 && len(cfg.CustomQuestions) == 0 {
		return errors.New("缺少测试数据集")
	}
	return nil
}

func (cfg *Config) Questions() []types.Question {
	var questions []types.Question

	questions = append(questions, cfg.datasetQuestions...)

	for _, question := range cfg.CustomQuestions {
		question.Dataset = "__custom_questions__"
		questions = append(questions, question)
	}

	return questions
}

// BenchmarkConfig 包含 benchmark 运行配置
type BenchmarkConfig struct {
	MaxTokens       int      `json:"max_tokens"`
	MaxWorkers      int      `json:"max_workers"`
	ReasoningEffort string   `json:"reasoning_effort"`
	Temperature     *float32 `json:"temperature"`
	TopP            *float32 `json:"top_p"`
}

func (cfg *Config) BenchmarkConfig() (conf BenchmarkConfig) {
	conf = BenchmarkConfig{
		MaxTokens:       util.Ternary(cfg.MaxTokens > 0, cfg.MaxTokens, 65536),
		MaxWorkers:      max(cfg.MaxWorkers, 1),
		ReasoningEffort: cfg.ReasoningEffort,
	}
	if cfg.Temperature != nil && *cfg.Temperature >= 0.0 && *cfg.Temperature <= 2.0 {
		conf.Temperature = cfg.Temperature
	}
	if cfg.TopP != nil && *cfg.TopP >= 0.0 && *cfg.TopP <= 1.0 {
		conf.TopP = cfg.TopP
	}
	return
}
