package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	defaultBaseURL     = "https://supertoken.shop"
	defaultDuration    = 60 * time.Second
	defaultConcurrency = "500,1000,2000,5000,10000,20000"
	excludedModel      = "gpt-image-2"

	defaultPrompt       = "Explain in plain English what API latency and throughput mean for a developer integrating LLM APIs. Write about 120 words. Do not use bullet points."
	defaultImgPmt       = "A single red circle on white background, minimal flat design."
	defaultPromptTokens = 2000
)

func main() {
	var (
		baseURL          = flag.String("base-url", defaultBaseURL, "API 服务地址")
		token            = flag.String("token", "", "Bearer token（与 -token-file 二选一）")
		tokenFile        = flag.String("token-file", "", "包含 token 字符串数组的 JSON 文件路径，如 [\"tok1\",\"tok2\"]（与 -token 二选一）")
		modelsJSON       = flag.String("models", "", `模型列表 JSON，例：[{"claude-opus-4-6":"anthropic"},{"gpt-5.5":"openai"}]（必填）`)
		modelsFile       = flag.String("models-file", "", "包含模型列表 JSON 的文件路径（与 -models 二选一）")
		duration         = flag.Duration("duration", defaultDuration, "每个并发档位的测试持续时长")
		concurrencyInput = flag.String("concurrency", defaultConcurrency, "并发量，多个并发量用`,`分隔")
		prompt           = flag.String("prompt", defaultPrompt, "文本端点的测试 prompt（与 -dynamic-prompt、-codex-prompt 互斥，两者开启时忽略）")
		imagePrompt      = flag.String("image-prompt", defaultImgPmt, "图片生成端点的测试 prompt")
		dynamicPrompt    = flag.Bool("dynamic-prompt", false, "开启后，每次文本请求都用随机拼接的长文本替代 -prompt，用于长上下文压测（与 -prompt、-codex-prompt 互斥）")
		promptTokens     = flag.Int("prompt-tokens", defaultPromptTokens, "-dynamic-prompt 生成文本的目标近似 token 数")
		codexPrompt      = flag.Bool("codex-prompt", false, "开启后，用类 Codex 系统提示词加随机简短用户提问替代 -prompt，模拟多用户使用标准 AI Agent 开发工具发送高相似度内容的场景（与 -prompt、-dynamic-prompt 互斥）")
		output           = flag.String("output", "", "Excel 输出路径（空=自动生成时间戳文件名，如 bench-20260618T150405.xlsx）")
		noExcel          = flag.Bool("no-excel", false, "跳过 Excel 导出，仅打印终端报告")
		warmup           = flag.Bool("warmup", true, "正式测试前执行预热阶段（并发=10，使用 -warmup-duration 控制时长）")
		warmupDuration   = flag.Duration("warmup-duration", 10*time.Second, "预热阶段持续时长")
		cooldown         = flag.Duration("cooldown", 5*time.Second, "每个并发档位之间的冷却等待时间")
		noTUI            = flag.Bool("no-tui", false, "禁用 TUI，使用纯文本控制台输出（stdout 非终端时自动禁用）")
	)
	flag.Parse()

	report.SetExcludedModel(excludedModel)

	if err := validatePromptFlags(*dynamicPrompt, *codexPrompt); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	tokens, err := resolveTokens(*token, *tokenFile)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	raw := strings.TrimSpace(*modelsJSON)
	if raw == "" && *modelsFile != "" {
		data, err := os.ReadFile(*modelsFile)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "error: 读取 -models-file 失败: %v\n", err)
			os.Exit(1)
		}
		raw = strings.TrimSpace(string(data))
	}
	if raw == "" {
		_, _ = fmt.Fprintln(os.Stderr, "error: -models 或 -models-file 为必填参数")
		flag.Usage()
		os.Exit(1)
	}

	models, err := parseModels(raw)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: 解析模型列表失败: %v\n", err)
		os.Exit(1)
	}

	// 过滤排除名单
	var active []types.ModelSpec
	for _, m := range models {
		name := strings.ToLower(m.Name)
		if strings.Contains(name, excludedModel) {
			fmt.Printf("[skip] 已排除模型: %s\n", m.Name)
			continue
		}
		active = append(active, m)
	}
	if len(active) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: 过滤后无可测试模型")
		os.Exit(1)
	}

	var concurrency []int
	for v := range strings.SplitSeq(*concurrencyInput, ",") {
		num, err := strconv.Atoi(v)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "error: 无效的并发数配置")
			os.Exit(1)
		}
		concurrency = append(concurrency, num)
	}
	if len(concurrency) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "error: 无效的并发数配置")
		os.Exit(1)
	}

	cfg := types.BenchmarkConfig{
		BaseURL:          *baseURL,
		Tokens:           tokens,
		Models:           active,
		Concurrency:      concurrency,
		Duration:         *duration,
		Prompt:           *prompt,
		ImagePrompt:      *imagePrompt,
		DynamicPrompt:    *dynamicPrompt,
		PromptTokens:     *promptTokens,
		CodexPrompt:      *codexPrompt,
		Warmup:           *warmup,
		WarmupDuration:   *warmupDuration,
		CooldownDuration: *cooldown,
	}

	runAt := time.Now()

	var results []types.AggregatedMetrics
	if useTUI(*noTUI) {
		results, err = runWithTUI(cfg)
	} else {
		results, err = runWithConsole(cfg)
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

	if !*noExcel {
		outPath := *output
		if outPath == "" {
			outPath = fmt.Sprintf("bench-%s.xlsx", runAt.Format("20060102T150405"))
		}
		fmt.Printf("\n导出 Excel → %s\n", outPath)
		if err := report.ExportExcel(cfg, results, runAt, outPath); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Excel 导出失败: %v\n", err)
		} else {
			fmt.Printf("Excel 已保存: %s\n", outPath)
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

	state := reporter.NewTuiState()
	prog := tea.NewProgram(reporter.NewTuiModel(state, cancel, cfg), tea.WithAltScreen())

	type outcome struct {
		results []types.AggregatedMetrics
		err     error
	}
	resCh := make(chan outcome, 1)
	rep := &reporter.TUIReporter{State: state, Prog: prog}
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

// validatePromptFlags 校验 -prompt、-dynamic-prompt、-codex-prompt 三者互斥。
// -dynamic-prompt 与 -codex-prompt 不可同时开启；若用户在命令行显式指定了
// -prompt（-prompt 本身带非空默认值，未显式指定时会被二者静默忽略），
// 也不可与 -dynamic-prompt、-codex-prompt 中的任意一个同时使用。
func validatePromptFlags(dynamicPrompt, codexPrompt bool) error {
	var promptSetExplicitly bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "prompt" {
			promptSetExplicitly = true
		}
	})

	trues := countTrues(promptSetExplicitly, dynamicPrompt, codexPrompt)
	if trues > 1 {
		return fmt.Errorf("-prompt, -dynamic-prompt, -codex-prompt 不可同时使用")
	}

	return nil
}

func countTrues(b ...bool) (count int) {
	count = 0
	for _, v := range b {
		if v {
			count++
		}
	}
	return count
}

// resolveTokens 从 -token 或 -token-file 构建 token 列表，两者互斥且必填其一。
func resolveTokens(single, filePath string) ([]string, error) {
	single = strings.TrimSpace(single)
	filePath = strings.TrimSpace(filePath)

	if single != "" && filePath != "" {
		return nil, fmt.Errorf("-token 与 -token-file 不可同时使用")
	}
	if single != "" {
		return []string{single}, nil
	}
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("读取 -token-file 失败: %w", err)
		}
		var tokens []string
		if err := json.Unmarshal(data, &tokens); err != nil {
			return nil, fmt.Errorf("-token-file 格式错误，期望字符串数组: %w", err)
		}
		var valid []string
		for _, t := range tokens {
			if s := strings.TrimSpace(t); s != "" {
				valid = append(valid, s)
			}
		}
		if len(valid) == 0 {
			return nil, fmt.Errorf("-token-file 中未找到有效 token")
		}
		return valid, nil
	}
	return nil, fmt.Errorf("-token 或 -token-file 为必填参数")
}

// parseModels 解析 JSON 格式的模型列表，如 [{"claude-opus-4-6":"anthropic"}]。
// 每个对象恰好一个键：模型名 → provider。
func parseModels(raw string) ([]types.ModelSpec, error) {
	var items []map[string]string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("期望 JSON 数组: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("模型数组为空")
	}

	var models []types.ModelSpec
	for i, item := range items {
		for name, prov := range item {
			p := types.Provider(strings.ToLower(strings.TrimSpace(prov)))
			if !runner.IsSupportedProvider(p) {
				return nil, fmt.Errorf("第 %d 项：未知 provider %q（合法值：anthropic / openai / openai-image / openai-response / gemini / __baseline__）", i, prov)
			}
			models = append(models, types.ModelSpec{Name: name, Provider: p})
		}
	}
	return models, nil
}
