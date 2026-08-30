package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
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
	logger.SetLogfileForReportDir(reportDir)

	// 请求错误日志（JSONL）随报告目录存放，只有出现错误时才会创建文件
	errlog.Init(filepath.Join(reportDir, "request_errors.jsonl"))

	var (
		benchmarkCfg = cfg.BenchmarkConfig()
		questions    = cfg.Questions()
	)

	logger.Printf("Loaded %d questions", len(questions))
	logger.Printf("Config: max_tokens=%d, max_workers=%d", benchmarkCfg.MaxTokens, benchmarkCfg.MaxWorkers)
	logger.Printf("Model: %s, Base URL: %s", cfg.Model, cfg.BaseURL)

	// 创建 OpenAI 客户端；Transport 包一层请求错误记录（传输失败/非 2xx/流中断）
	clientConfig := openai.DefaultConfig(cfg.APIKey)
	clientConfig.BaseURL = cfg.BaseURL
	clientConfig.HTTPClient = &http.Client{Transport: errlog.WrapTransport(nil)}
	client := openai.NewClientWithConfig(clientConfig)

	// 运行 benchmark
	logger.Printf("Benchmark started")
	runStart := time.Now()
	results := runner.RunBenchmark(client, cfg.Model, questions, benchmarkCfg)
	elapsed := time.Since(runStart) // 整批运行的墙钟耗时，用于 System TPS/TPM
	logger.Printf("Benchmark finished")
	if n := errlog.Count(); n > 0 {
		logger.Printf("请求错误日志（%d 条）: %s", n, errlog.Path())
	}

	report.OutputResults(results, reportDir)         // 输出 JSON 结果
	report.SaveIndividualReports(results, reportDir) // 保存每个问题的详细报告
	reporter.PrintStatistics(results, elapsed)       // 计算统计信息
}
