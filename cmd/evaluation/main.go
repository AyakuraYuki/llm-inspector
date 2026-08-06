// 大语言模型可用性评测工具的 CLI 入口。
//
// 用法:
//
//	go run ./main.go run --config eval.yaml [--layers L1,L3] [--output ./reports] [--dataset data.yaml]
//	go run ./main.go list
//	go run ./main.go version
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

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/runner"
)

var programName string

func main() {
	if exe, err := os.Executable(); err == nil {
		programName = filepath.Base(exe)
	}
	if programName == "" {
		programName = filepath.Base(os.Args[0])
	}

	if len(os.Args) < 2 {
		usage(programName)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(os.Args[2:]))
	case "list":
		listCmd()
	case "version":
		fmt.Println(programName, runner.Version)
	case "-h", "--help", "help":
		usage(programName)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "未知命令: %s\n", os.Args[1])
		usage(programName)
		os.Exit(2)
	}
}

func usage(programName string) {
	_, _ = fmt.Fprintf(os.Stderr, `%[1]s — 大语言模型可用性评测工具

用法:
  %[1]s run --config <配置文件> [选项]   执行评测
  %[1]s list                            列出全部评测层与检查项
  %[1]s version                         显示版本

run 选项:
  --config    评测配置 YAML（必填）
  --layers    只执行指定层，逗号分隔，如 L1,L3（默认全部启用的层）
  --dataset   覆盖 L3 数据集路径
  --output    覆盖报告输出目录
`, programName)
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := fs.String("config", "", "评测配置 YAML（必填）")
	layersFlag := fs.String("layers", "", "只执行指定层，逗号分隔，如 L1,L3")
	dataset := fs.String("dataset", "", "覆盖 L3 数据集路径")
	output := fs.String("output", "", "覆盖报告输出目录")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		_, _ = fmt.Fprintln(os.Stderr, "错误: 缺少 --config")
		return 2
	}

	cfg, err := config.Load(*configPath, programName)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "配置错误:", err)
		return 2
	}
	if *dataset != "" {
		cfg.Layers.Capability.Dataset = *dataset
	}
	if *output != "" {
		cfg.Output.Dir = *output
	}

	var only map[string]bool
	if *layersFlag != "" {
		only = map[string]bool{}
		for id := range strings.SplitSeq(*layersFlag, ",") {
			id = strings.ToUpper(strings.TrimSpace(id))
			if !strings.HasPrefix(id, "L") {
				id = "L" + id
			}
			only[id] = true
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("开始评测 %s（模型 %s）...\n", cfg.Target.BaseURL, cfg.Target.Model)
	start := time.Now()
	r, err := runner.Run(ctx, cfg, only)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "评测失败:", err)
		return 1
	}
	report.Console(os.Stdout, r)

	outDir, err := report.Save(cfg.Output.Dir, cfg.Output.Formats, r)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "保存报告失败:", err)
		return 1
	}
	fmt.Printf("报告已保存: %s（总耗时 %.1fs）\n", outDir, time.Since(start).Seconds())

	if r.Verdict != "pass" && r.Verdict != "pass_with_warnings" {
		return 1
	}
	return 0
}

func listCmd() {
	fmt.Println("评测层与检查项：")
	for _, li := range runner.Catalog() {
		fmt.Printf("\n%s %s\n", li.ID, li.Name)
		for _, c := range li.Checks {
			fmt.Printf("  - %s\n", c)
		}
	}
}
