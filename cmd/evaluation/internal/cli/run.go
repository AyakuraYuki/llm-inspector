package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/runner"
)

var configPath string

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "执行评测",
	RunE: func(cmd *cobra.Command, _ []string) error {
		conf, err := config.Load(configPath, rootCmd.Name())
		if err != nil {
			return &ExitError{Code: 2, Err: fmt.Errorf("配置错误: %w", err)}
		}

		ctx := cmd.Context() // from main

		fmt.Printf("开始评测 %s（模型 %s）...\n", conf.Target.BaseURL, conf.Target.Model)
		start := time.Now()
		r, err := runner.Run(ctx, conf)
		if err != nil {
			return &ExitError{Code: 1, Err: fmt.Errorf("评测失败: %w", err)}
		}
		report.Console(os.Stdout, r)

		outDir, err := report.Save(conf.Output.Dir, conf.Output.Formats, r)
		if err != nil {
			return &ExitError{Code: 1, Err: fmt.Errorf("保存报告失败: %w", err)}
		}
		fmt.Printf("报告已保存: %s（总耗时 %.1fs）\n", outDir, time.Since(start).Seconds())

		if r.Verdict != "pass" && r.Verdict != "pass_with_warnings" {
			return &ExitError{Code: 1, Err: errors.New("评测未通过")}
		}
		return nil
	},
}

func init() {
	runCmd.Flags().StringVar(&configPath, "config", "", "评测配置 YAML 文件路径（必需）")
	_ = runCmd.MarkFlagRequired("config") // mark `config` flag as required
	rootCmd.AddCommand(runCmd)            // register this sub command
}
