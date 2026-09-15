package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/samplelog"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
)

const (
	defaultConfigPath = "config.yaml"
)

func main() {
	configPath := flag.String("config", defaultConfigPath, "YAML 配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	bench := cfg.ToBenchmark()

	// 过滤排除名单（单机版当前无排除项，整体复制）
	active := append([]types.ModelSpec(nil), bench.Models...)
	if len(active) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: 过滤后无可测试模型")
		os.Exit(1)
	}
	bench.Models = active

	startAt := time.Now()

	// 请求错误日志与 Excel 报告同前缀，只有出现错误时才会创建文件
	errlog.Init(fmt.Sprintf("bench-%s-request-errors.jsonl", startAt.Format("20060102T150405")))
	// 原始样本导出（可选）：逐档位追加，档位结束即落盘，中途退出也不丢已完成档位
	samplelog.Init(cfg.SampleOutput)

	var results []types.AggregatedMetrics
	if useTUI(cfg.NoTUI) {
		results, err = runWithTUI(bench)
	} else {
		results, err = runWithConsole(bench)
	}
	samplelog.Close()
	printErrlogNotice()
	printSampleNotice()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("无已完成的测试结果，跳过报告输出。")
		return
	}

	report.PrintReport(results)

	if cfg.JSONOutput != "" {
		if err := report.ExportJSON(bench, results, startAt, cfg.JSONOutput); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "JSON 导出失败: %v\n", err)
		} else {
			fmt.Printf("JSON 已保存: %s\n", cfg.JSONOutput)
		}
	}
	if cfg.CSVOutput != "" {
		if err := report.ExportCSV(results, cfg.CSVOutput); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "CSV 导出失败: %v\n", err)
		} else {
			fmt.Printf("CSV 已保存: %s\n", cfg.CSVOutput)
		}
	}

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

// printErrlogNotice 在压测结束（含中止/预检失败）后提示请求错误日志的位置。
func printErrlogNotice() {
	if n := errlog.Count(); n > 0 {
		fmt.Printf("\n请求错误日志（%d 条）: %s\n", n, errlog.Path())
	}
}

// printSampleNotice 在压测结束后提示原始样本文件的位置（未开启导出时不输出）。
func printSampleNotice() {
	if n := samplelog.Count(); n > 0 {
		fmt.Printf("原始样本（%d 条）: %s\n", n, samplelog.Path())
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
	fmt.Printf("Tokens      : model-scoped token groups\n")
	fmt.Printf("Duration    : %s per concurrency level\n", cfg.Duration)
	fmt.Printf("Concurrency : %v\n", cfg.Concurrency)
	if cfg.OpenLoop {
		bound := "在途上限=concurrency"
		if cfg.OpenLoopUnbounded {
			bound = "无界，不设在途上限"
		}
		fmt.Printf("Load Mode   : open-loop（目标 RPS：%v，泊松到达，%s）\n", cfg.RequestRate, bound)
	} else {
		fmt.Printf("Load Mode   : closed-loop（默认；高负载下尾延迟可能被低估，见 README「Coordinated Omission」）\n")
	}
	warmupLabel := "disabled"
	if cfg.Warmup {
		warmupLabel = cfg.WarmupDuration.String()
	}
	if cfg.Warmup && cfg.WarmupPerLevel {
		warmupLabel += " (before every concurrency level)"
	}
	fmt.Printf("Warmup      : %s\n", warmupLabel)
	fmt.Printf("Requests    : %s\n", requestsLabel(cfg))
	fmt.Printf("Cooldown    : %s between levels\n", cfg.CooldownDuration)
	fmt.Printf("Think Time  : %s (closed-loop only)\n", thinkTimeLabel(cfg.ThinkTime))
	fmt.Printf("Max Tokens  : %d per request\n", cfg.EffectiveMaxOutputTokens())
	earlyStopLabel := "disabled"
	if cfg.EarlyStopEnabled {
		skip := "not skipping higher concurrency"
		if cfg.SkipHigherConcurrency {
			skip = "skip higher concurrency"
		}
		earlyStopLabel = fmt.Sprintf("max_error_rate=%.1f%%, min_samples=%d, %s", cfg.MaxErrorRate*100, cfg.MinSamples, skip)
	}
	fmt.Printf("Early Stop  : %s\n", earlyStopLabel)
	fmt.Printf("Models (%d):\n", len(cfg.Models))
	for _, m := range cfg.Models {
		fmt.Printf("  - %-32s [%s]  group=%s  (%d keys)\n", m.Name, m.Provider, m.TokenGroup, len(m.Tokens))
	}
	switch {
	case cfg.DatasetPrompt:
		fmt.Printf("Prompt      : dataset (line_by_line, %d lines, one random line per request)\n", len(cfg.DatasetLines))
	case cfg.DynamicPrompt:
		fmt.Printf("Prompt      : %s\n", dynamicPromptLabel(cfg))
	case cfg.CodexPrompt:
		fmt.Printf("Prompt      : codex-style (fixed system prompt + random short question, simulating high-similarity agent traffic)\n")
	default:
		fmt.Printf("Prompt      : %s\n", cfg.Prompt)
	}
	fmt.Printf("Tokenizer   : %s\n", tokenizerLabel(cfg))
	fmt.Printf("Image Prompt: %s\n", cfg.ImagePrompt)
	fmt.Printf("\n")
}

// thinkTimeLabel 渲染配置头里的思考时间：0 要显式说明是「完成即发」，
// 否则读报告的人无法区分「没配」和「配成了 0」。
func thinkTimeLabel(d time.Duration) string {
	if d <= 0 {
		return "0s (fire as soon as the previous response completes)"
	}
	return d.String()
}

// dynamicPromptLabel 渲染 dynamic 模式的配置头：长度是单点还是区间、精度是字符近似还是分词器收敛。
func dynamicPromptLabel(cfg types.BenchmarkConfig) string {
	length := fmt.Sprintf("~%d tokens", cfg.PromptTokens)
	if cfg.PromptTokensMin > 0 && cfg.PromptTokensMax > 0 {
		length = fmt.Sprintf("[%d, %d] tokens uniformly sampled", cfg.PromptTokensMin, cfg.PromptTokensMax)
	}
	precision := "char-approximated"
	if cfg.TokenizerPath != "" {
		precision = "tokenizer-exact"
	}
	return fmt.Sprintf("dynamic (%s, %s, randomized per request)", length, precision)
}

// tokenizerLabel 渲染本地分词器配置：路径、词表指纹前缀、usage 对拍阈值。
func tokenizerLabel(cfg types.BenchmarkConfig) string {
	if cfg.TokenizerPath == "" {
		return "none (char-based estimates, no usage cross-check)"
	}
	fp := cfg.TokenizerFingerprint
	if len(fp) > 12 {
		fp = fp[:12]
	}
	drift := "usage cross-check off"
	if cfg.UsageDriftPct > 0 {
		drift = fmt.Sprintf("usage drift threshold %.1f%%", cfg.UsageDriftPct)
	}
	return fmt.Sprintf("%s (vocab sha256 %s…, %s)", cfg.TokenizerPath, fp, drift)
}

// requestsLabel 渲染运行形态：纯时长制，还是请求数与时长先到者结束。
func requestsLabel(cfg types.BenchmarkConfig) string {
	switch len(cfg.RequestsPerLevel) {
	case 0:
		return "duration-based (no per-level request cap)"
	case 1:
		return fmt.Sprintf("%d per level, or duration, whichever comes first", cfg.RequestsPerLevel[0])
	default:
		return fmt.Sprintf("%v per level (one per concurrency level), or duration, whichever comes first", cfg.RequestsPerLevel)
	}
}
