// imagespec 按 OpenAI 官方接口口径（docs/create_image-openai-api-spec.md）
// 测试 gpt-image-2 在各种请求参数下的正确性：官方允许的参数必须成功生成图片，
// 官方禁止的参数必须被拒绝。用于排查供应商放宽参数限制的问题，
// 例如本不允许的 4096x4096 超分生成。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/cases"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/imagespec/internal/runner"
)

const defaultConfigPath = "config.yaml"

func main() {
	configPath := flag.String("config", defaultConfigPath, "YAML 配置文件路径")
	listOnly := flag.Bool("list", false, "仅列出筛选后的测试用例，不发起请求")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cs := cases.Filter(cases.Build(), cfg.Only, cfg.Skip, cfg.IncludeProbe)
	if len(cs) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: only/skip 筛选后无测试用例")
		os.Exit(1)
	}

	if *listOnly {
		report.PrintCaseList(cs)
		return
	}

	if cfg.APIKey == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: 缺少 api_key（在配置文件填写，或设置环境变量 OPENAI_API_KEY）")
		os.Exit(1)
	}

	printHeader(cfg, cs)

	// 第一次 Ctrl+C 优雅中止（保留已执行部分的结果），第二次直接终止进程
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	defer stop()

	startAt := time.Now()
	results := runner.Run(ctx, cfg, cs)

	if ctx.Err() != nil {
		fmt.Println("\n[中止] 测试被用户中断，以下为已执行部分的结果。")
	}
	report.Print(results)

	outPath := cfg.Output
	if outPath == "" {
		outPath = fmt.Sprintf("imagespec-%s.json", startAt.Format("20060102T150405"))
	}
	if err := report.WriteJSON(outPath, cfg, startAt, results); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "JSON 报告写入失败: %v\n", err)
	} else {
		fmt.Printf("\nJSON 报告已保存: %s\n", outPath)
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
	fmt.Printf("\ngpt-image-2 参数合规性测试 (imagespec)\n")
	fmt.Printf("========================================\n")
	fmt.Printf("Base URL    : %s\n", cfg.BaseURL)
	fmt.Printf("Model       : %s\n", cfg.Model)
	fmt.Printf("Cases       : %d（预期成功 %d / 预期拒绝 %d / 观察 %d）\n",
		len(cs), byExpect[cases.ExpectSuccess], byExpect[cases.ExpectReject], byExpect[cases.ExpectObserve])
	fmt.Printf("Concurrency : %d\n", cfg.Concurrency)
	fmt.Printf("Timeout     : %s\n", cfg.Timeout)
	if cfg.SaveImages != "" {
		fmt.Printf("Save Images : %s\n", cfg.SaveImages)
	}
	fmt.Printf("\n")
}
