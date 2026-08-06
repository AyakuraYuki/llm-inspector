package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	defaultConfigPath = "config.yaml"
	excludedModel     = "gpt-image-2"
)

func main() {
	configPath := flag.String("config", defaultConfigPath, "YAML 配置文件路径")
	flag.Parse()

	report.SetExcludedModel(excludedModel)

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	bench := cfg.ToBenchmark()

	// 过滤排除名单
	var active []types.ModelSpec
	for _, m := range bench.Models {
		if strings.Contains(strings.ToLower(m.Name), excludedModel) {
			fmt.Printf("[skip] 已排除模型: %s\n", m.Name)
			continue
		}
		active = append(active, m)
	}
	if len(active) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: 过滤后无可测试模型")
		os.Exit(1)
	}
	bench.Models = active

	startAt := time.Now()

	var results []types.AggregatedMetrics
	if useTUI(cfg.NoTUI) {
		results, err = runWithTUI(bench)
	} else {
		results, err = runWithConsole(bench)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("无已完成的测试结果，跳过报告输出。")
		return
	}

	report.PrintReport(results)

	if cfg.NoExcel {
		return // 跳过导出excel报告的流程
	}

	// 导出excel
	outPath := cfg.Output
	if outPath == "" {
		outPath = fmt.Sprintf("bench-%s.xlsx", startAt.Format("20060102T150405"))
	}
	fmt.Printf("\n导出 Excel → %s\n", outPath)
	if err := report.ExportExcel(bench, results, startAt, outPath); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Excel 导出失败: %v\n", err)
	} else {
		fmt.Printf("Excel 已保存: %s\n", outPath)
	}
}

// useTUI 判断是否启用 TUI：未显式禁用且 stdout 是终端。
func useTUI(noTUI bool) bool {
	if noTUI {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runWithConsole 以纯文本控制台模式运行压测。
// 第一次 Ctrl+C 优雅中止（返回已完成部分结果），第二次直接终止进程。
func runWithConsole(cfg types.BenchmarkConfig) ([]types.AggregatedMetrics, error) {
	printHeader(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // 解除信号接管，让第二次 Ctrl+C 恢复默认行为（直接杀进程）
	}()
	defer stop()

	return runner.RunBenchmark(ctx, cfg, &reporter.ConsoleReporter{})
}

// runWithTUI 启动 TUI，压测在后台协程中运行，事件通过 tuiReporter 写入共享状态。
// TUI 退出后（正常结束或用户中止）再把配置头和报告打印到普通终端输出里，方便留存。
func runWithTUI(cfg types.BenchmarkConfig) ([]types.AggregatedMetrics, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		results []types.AggregatedMetrics
		err     error
	}

	var (
		state = reporter.NewTuiState()
		prog  = tea.NewProgram(reporter.NewTuiModel(state, cancel, cfg), tea.WithAltScreen())
		resCh = make(chan outcome, 1)
		rep   = &reporter.TUIReporter{State: state, Prog: prog}
	)

	go func() {
		results, err := runner.RunBenchmark(ctx, cfg, rep)
		resCh <- outcome{results, err}
		prog.Send(reporter.BenchDoneMsg{})
	}()

	if _, err := prog.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "TUI 运行失败: %v\n", err)
	}
	cancel() // TUI 异常退出时确保压测协程也能收敛
	out := <-resCh

	state.Lock()
	aborted := state.IsAborted()
	state.Unlock()

	printHeader(cfg)
	if aborted && out.err == nil {
		fmt.Println("[中止] 压测被用户中止，以下为已完成部分的结果。")
	}
	return out.results, out.err
}

func printHeader(cfg types.BenchmarkConfig) {
	fmt.Printf("\nNewAPI API Benchmark\n")
	fmt.Printf("========================\n")
	fmt.Printf("Base URL    : %s\n", cfg.BaseURL)
	fmt.Printf("Tokens      : %d token(s)\n", len(cfg.Tokens))
	fmt.Printf("Duration    : %s per concurrency level\n", cfg.Duration)
	fmt.Printf("Concurrency : %v\n", cfg.Concurrency)
	warmupLabel := "disabled"
	if cfg.Warmup {
		warmupLabel = cfg.WarmupDuration.String()
	}
	fmt.Printf("Warmup      : %s\n", warmupLabel)
	fmt.Printf("Cooldown    : %s between levels\n", cfg.CooldownDuration)
	fmt.Printf("Models (%d):\n", len(cfg.Models))
	for _, m := range cfg.Models {
		fmt.Printf("  - %-32s [%s]\n", m.Name, m.Provider)
	}
	switch {
	case cfg.DynamicPrompt:
		fmt.Printf("Prompt      : dynamic (~%d tokens, randomized per request)\n", cfg.PromptTokens)
	case cfg.CodexPrompt:
		fmt.Printf("Prompt      : codex-style (fixed system prompt + random short question, simulating high-similarity agent traffic)\n")
	default:
		fmt.Printf("Prompt      : %s\n", cfg.Prompt)
	}
	fmt.Printf("Image Prompt: %s\n", cfg.ImagePrompt)
	fmt.Printf("\n")
}
