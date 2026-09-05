// performance-cluster 是 cmd/performance 的多机分布式版本：
// 一个二进制两个角色——`agent` 在各压测节点上常驻监听，`run` 作为
// coordinator 读取配置、把并发档位切分到各 agent、轮询聚合进度、
// 回收原始样本统一聚合后输出与单机版口径一致的报告。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/agentd"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/coord"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	defaultConfigPath = "config.yaml"
	excludedModel     = "gpt-image-2" // 与单机版一致的硬编码排除名单
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "agent":
		agentMain(os.Args[2:])
	case "run":
		runMain(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		_, _ = fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`performance-cluster —— 多机分布式压测（cmd/performance 的集群版本）

用法:
  performance-cluster agent -listen :7070 [-token <secret>]
      在压测节点上启动常驻 agent 守护进程

  performance-cluster run [-config config.yaml]
      作为 coordinator 运行压测（配置需包含 cluster.agents 列表）
`)
}

// ── agent 子命令 ─────────────────────────────────────────────────────────────

func agentMain(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	listen := fs.String("listen", ":7070", "监听地址（host:port）")
	token := fs.String("token", "", "可选的共享密钥，与 coordinator 配置的 cluster.auth_token 一致")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := agentd.NewServer(*token).Run(ctx, *listen); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ── run 子命令（coordinator）─────────────────────────────────────────────────

func runMain(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "YAML 配置文件路径")
	_ = fs.Parse(args)

	report.SetExcludedModel(excludedModel)

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if cfg.Cluster == nil {
		_, _ = fmt.Fprintln(os.Stderr, "error: 配置缺少 cluster 段（cluster.agents 为分布式压测的必填项）")
		os.Exit(1)
	}

	bench := cfg.ToBenchmark()

	// 过滤排除名单（与单机版一致）
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

	var (
		results []types.AggregatedMetrics
		summary *coord.RunSummary
		runErr  error
	)
	if useTUI(cfg.NoTUI) {
		results, summary, runErr = runWithTUI(bench, cfg.Cluster)
	} else {
		results, summary, runErr = runWithConsole(bench, cfg.Cluster)
	}

	printAgentErrlogNotices(summary)
	if runErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		if len(results) == 0 {
			os.Exit(1)
		}
		fmt.Println("[错误] 压测中途失败，以下为已完成部分的结果。")
	}
	if len(results) == 0 {
		fmt.Println("无已完成的测试结果，跳过报告输出。")
		return
	}

	report.PrintReport(results)

	if !cfg.NoExcel {
		outPath := cfg.Output
		if outPath == "" {
			outPath = fmt.Sprintf("bench-cluster-%s.xlsx", startAt.Format("20060102T150405"))
		}
		report.SetOverviewExtra(clusterOverviewRows(summary))
		fmt.Printf("\n导出 Excel → %s\n", outPath)
		if err := report.ExportExcel(bench, results, startAt, outPath); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Excel 导出失败: %v\n", err)
		} else {
			fmt.Printf("Excel 已保存: %s\n", outPath)
		}
	}
	if runErr != nil {
		os.Exit(1)
	}
}

// clusterOverviewRows 生成 Excel 总览 sheet 的集群信息行。
func clusterOverviewRows(summary *coord.RunSummary) [][2]string {
	if summary == nil {
		return nil
	}
	rows := [][2]string{{"集群节点", fmt.Sprintf("%d 个 agent（concurrency 为全局总并发，按节点切分）", len(summary.Agents))}}
	for _, a := range summary.Agents {
		rows = append(rows, [2]string{"  节点 " + a.Addr, fmt.Sprintf("最大并发分片 %d", a.MaxShare)})
	}
	return rows
}

// printAgentErrlogNotices 打印各 agent 本机的请求错误日志位置。
func printAgentErrlogNotices(summary *coord.RunSummary) {
	if summary == nil {
		return
	}
	for _, a := range summary.Agents {
		if a.ErrlogCount > 0 {
			fmt.Printf("\nagent %s 请求错误日志（%d 条）: %s\n", a.Addr, a.ErrlogCount, a.ErrlogPath)
		}
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
func runWithConsole(bench types.BenchmarkConfig, cluster *config.ClusterConfig) ([]types.AggregatedMetrics, *coord.RunSummary, error) {
	printHeader(bench, cluster)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // 解除信号接管，让第二次 Ctrl+C 恢复默认行为（直接杀进程）
	}()
	defer stop()

	return coord.RunBenchmark(ctx, bench, cluster, &reporter.ConsoleReporter{})
}

// runWithTUI 启动 TUI，压测在后台协程中运行，事件通过 tuiReporter 写入共享状态。
// TUI 退出后（正常结束或用户中止）再把配置头和报告打印到普通终端输出里，方便留存。
func runWithTUI(bench types.BenchmarkConfig, cluster *config.ClusterConfig) ([]types.AggregatedMetrics, *coord.RunSummary, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		results []types.AggregatedMetrics
		summary *coord.RunSummary
		err     error
	}

	var (
		state = reporter.NewTuiState()
		prog  = tea.NewProgram(reporter.NewTuiModel(state, cancel, bench), tea.WithAltScreen())
		resCh = make(chan outcome, 1)
		rep   = &reporter.TUIReporter{State: state, Prog: prog}
	)

	go func() {
		results, summary, err := coord.RunBenchmark(ctx, bench, cluster, rep)
		resCh <- outcome{results, summary, err}
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

	printHeader(bench, cluster)
	if aborted && out.err == nil {
		fmt.Println("[中止] 压测被用户中止，以下为已完成部分的结果。")
	}
	return out.results, out.summary, out.err
}

func printHeader(bench types.BenchmarkConfig, cluster *config.ClusterConfig) {
	fmt.Printf("\nNewAPI API Benchmark (Cluster)\n")
	fmt.Printf("==============================\n")
	fmt.Printf("Base URL    : %s\n", bench.BaseURL)
	fmt.Printf("Agents (%d) : %s\n", len(cluster.Agents), strings.Join(cluster.Agents, ", "))
	fmt.Printf("Tokens      : model-scoped token groups\n")
	fmt.Printf("Duration    : %s per concurrency level\n", bench.Duration)
	fmt.Printf("Concurrency : %v (global, split across agents)\n", bench.Concurrency)
	warmupLabel := "disabled"
	if bench.Warmup {
		warmupLabel = bench.WarmupDuration.String()
	}
	fmt.Printf("Warmup      : %s\n", warmupLabel)
	fmt.Printf("Cooldown    : %s between levels\n", bench.CooldownDuration)
	earlyStopLabel := "disabled"
	if bench.EarlyStopEnabled {
		skip := "not skipping higher concurrency"
		if bench.SkipHigherConcurrency {
			skip = "skip higher concurrency"
		}
		earlyStopLabel = fmt.Sprintf("max_error_rate=%.1f%%, min_samples=%d, %s (coordinator-aggregated)", bench.MaxErrorRate*100, bench.MinSamples, skip)
	}
	fmt.Printf("Early Stop  : %s\n", earlyStopLabel)
	fmt.Printf("Models (%d):\n", len(bench.Models))
	for _, m := range bench.Models {
		fmt.Printf("  - %-32s [%s]  group=%s  (%d keys)\n", m.Name, m.Provider, m.TokenGroup, len(m.Tokens))
	}
	switch {
	case bench.DynamicPrompt:
		fmt.Printf("Prompt      : dynamic (~%d tokens, randomized per request)\n", bench.PromptTokens)
	case bench.CodexPrompt:
		fmt.Printf("Prompt      : codex-style (fixed system prompt + random short question, simulating high-similarity agent traffic)\n")
	default:
		fmt.Printf("Prompt      : %s\n", bench.Prompt)
	}
	fmt.Printf("Image Prompt: %s\n", bench.ImagePrompt)
	fmt.Printf("\n")
}
