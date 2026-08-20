package main

import (
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

var (
	configPath string
)

func main() {
	flag.StringVar(&configPath, "config", "", "启动配置 YAML（必填）")
	flag.Parse()

	if configPath == "" {
		logger.Printf("错误: 缺少 -config")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Printf("配置错误: %v\n", err)
		os.Exit(1)
	}

	// 创建统一的报告目录，本次运行的所有输出都存放在此
	reportDir := filepath.Join(cfg.ReportDir, time.Now().Format("20060102_150405"))
	if err = os.MkdirAll(reportDir, os.ModePerm); err != nil {
		logger.Printf("Failed to create report directory: %v\n", err)
		os.Exit(1)
	}

	// 日志文件与报告目录同名，带 .txt 后缀，存放在启动时的工作目录
	logger.SetLogfile(filepath.Join(workingDir(), filepath.Base(reportDir)+".txt"))

	var (
		benchmarkCfg = cfg.BenchmarkConfig()
		questions    = cfg.Questions()
	)

	logger.Printf("Loaded %d questions", len(questions))
	logger.Printf("Config: max_tokens=%d, max_workers=%d", benchmarkCfg.MaxTokens, benchmarkCfg.MaxWorkers)
	logger.Printf("Model: %s, Base URL: %s", cfg.Model, cfg.BaseURL)

	// 创建 OpenAI 客户端
	clientConfig := openai.DefaultConfig(cfg.APIKey)
	clientConfig.BaseURL = cfg.BaseURL
	client := openai.NewClientWithConfig(clientConfig)

	// 运行 benchmark
	logger.Printf("Benchmark started")
	results := runner.RunBenchmark(client, cfg.Model, questions, benchmarkCfg)
	logger.Printf("Benchmark finished")

	report.OutputResults(results, reportDir)         // 输出 JSON 结果
	report.SaveIndividualReports(results, reportDir) // 保存每个问题的详细报告
	reporter.PrintStatistics(results)                // 计算统计信息
}

// workingDir 返回启动时的工作目录，获取失败时回退到相对路径
func workingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
