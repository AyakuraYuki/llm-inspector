// imagespec 按 OpenAI 官方接口口径（docs/create_image-openai-api-spec.md）
// 测试 GPT image 模型（gpt-image-2 及 gpt-image-2.5-sunburst/flare 系列）在
// 各种请求参数下的正确性：官方允许的参数必须成功生成图片，官方禁止的参数
// 必须被拒绝。用于排查供应商放宽参数限制的问题，例如本不允许的 4096x4096
// 超分生成。少数用例的预期结果因模型而异（如 quality=xhigh/max），按
// config.yaml 中的 model 字段自动分族，详见 internal/cases 包文档。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/cases"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

const defaultConfigPath = "config.yaml"

func main() {
	configPath := flag.String("config", defaultConfigPath, "YAML 配置文件路径")
	listOnly := flag.Bool("list", false, "仅列出筛选后的测试用例，不发起请求")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Printf("配置错误: %v", err)
		os.Exit(1)
	}

	cs := cases.Filter(cases.Build(cfg.Model), cfg.Only, cfg.Skip, cfg.IncludeProbe)
	if len(cs) == 0 {
		logger.Printf("错误: only/skip 筛选后无测试用例")
		os.Exit(1)
	}

	if *listOnly {
		report.PrintCaseList(cs)
		return
	}

	if cfg.APIKey == "" {
		logger.Printf("错误: 缺少 api_key（在配置文件填写，或设置环境变量 OPENAI_API_KEY）")
		os.Exit(1)
	}

	startAt := time.Now()
	outPath := cfg.Output
	if outPath == "" {
		outPath = fmt.Sprintf("imagespec-%s.json", startAt.Format("20060102T150405"))
	}
	// 日志文件与 JSON 报告同名（去掉扩展名），带 .txt 后缀，存放在当前工作目录
	logger.SetLogfileForReportDir(strings.TrimSuffix(outPath, filepath.Ext(outPath)))

	printHeader(cfg, cs)

	// 第一次 Ctrl+C 优雅中止（保留已执行部分的结果），第二次直接终止进程
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	defer stop()

	results := runner.Run(ctx, cfg, cs)
	elapsed := time.Since(startAt)

	if ctx.Err() != nil {
		logger.Printf("[中止] 测试被用户中断，以下为已执行部分的结果。")
	}
	report.Print(results, elapsed)
	report.PrintSpeed(results)

	if err := report.WriteJSON(outPath, cfg, startAt, elapsed, results); err != nil {
		logger.Printf("JSON 报告写入失败: %v", err)
	} else {
		logger.Printf("JSON 报告已保存: %s", outPath)
	}

	// 存在 FAIL 时以非零码退出，便于脚本/CI 判断
	if report.HasFailure(results) {
		os.Exit(1)
	}
}

func printHeader(cfg *config.Config, cs []cases.Case) {
	byExpect := map[cases.Expectation]int{}
	for _, c := range cs {
		byExpect[c.Expect]++
	}
	logger.Printf("GPT image 参数合规性测试 (imagespec)")
	logger.Printf("========================================")
	logger.Printf("Base URL    : %s", cfg.BaseURL)
	model := cfg.Model
	if cases.IsGPTImage25(cfg.Model) {
		model += "（按 gpt-image-2.5 系列口径：quality=xhigh/max 视为合法）"
	}
	logger.Printf("Model       : %s", model)
	logger.Printf("Cases       : %d（预期成功 %d / 预期拒绝 %d / 观察 %d）",
		len(cs), byExpect[cases.ExpectSuccess], byExpect[cases.ExpectReject], byExpect[cases.ExpectObserve])
	logger.Printf("Concurrency : %d", cfg.Concurrency)
	logger.Printf("Timeout     : %s", cfg.Timeout)
	if cfg.SaveImages != "" {
		logger.Printf("Save Images : %s", cfg.SaveImages)
	}
	logger.Printf("")
}
