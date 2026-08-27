package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/report"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/internal/errlog"
	"github.com/AyakuraYuki/llm-inspector/internal/logger"
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

		// 先确定本次报告目录，日志文件与其同名并存放在当前工作目录
		outDir := report.NewOutputDir(conf.Output.Dir)
		logger.SetLogfileForReportDir(outDir)

		// 请求错误日志（JSONL）随报告目录存放，只有出现错误时才会创建文件
		errlog.Init(filepath.Join(outDir, "request_errors.jsonl"))

		logger.Printf("开始评测 %s（模型 %s）...", conf.Target.BaseURL, conf.Target.Model)
		start := time.Now()
		r, err := runner.Run(ctx, conf)
		if err != nil {
			return &ExitError{Code: 1, Err: fmt.Errorf("评测失败: %w", err)}
		}
		report.Console(os.Stdout, r)

		if err = report.Save(outDir, conf.Output.Formats, r); err != nil {
			return &ExitError{Code: 1, Err: fmt.Errorf("保存报告失败: %w", err)}
		}
		logger.Printf("报告已保存: %s（总耗时 %.1fs）", outDir, time.Since(start).Seconds())
		if n := errlog.Count(); n > 0 {
			logger.Printf("请求错误日志（%d 条）: %s", n, errlog.Path())
		}

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
