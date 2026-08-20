// Package report 负责评测报告的终端展示与文件输出（JSON / Markdown）。
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// Console 向 w 打印终端汇总。
func Console(w io.Writer, r *types.Report) {
	renderConsole(w, r)
}

// VerdictLabel 返回结论的可读标注。
func VerdictLabel(v string) string {
	switch v {
	case "pass":
		return "pass（接入与冒烟全部达标）"
	case "pass_with_warnings":
		return "pass_with_warnings（接入达标，冒烟存在短板）"
	case "fail":
		return "fail（接入结论未通过）"
	case "abort":
		return "abort（L1 门控未通过，已中止）"
	case "no_layers_executed":
		return "no_layers_executed（未执行任何层）"
	default:
		return v
	}
}

// NewOutputDir 生成本次运行的报告输出目录路径（dir/<时间戳>），不创建目录。
// 目录名同时用于日志文件命名，需要在评测开始前先行确定，因此与 Save 分离。
func NewOutputDir(dir string) string {
	return filepath.Join(dir, time.Now().Format("20060102_150405"))
}

// Save 按 formats 将报告写入 outDir，目录不存在时创建。
func Save(outDir string, formats []string, r *types.Report) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("创建报告目录失败: %w", err)
	}
	for _, f := range formats {
		switch strings.ToLower(f) {
		case "json":
			if err := writeJSON(filepath.Join(outDir, "report.json"), r); err != nil {
				return err
			}
		case "markdown", "md":
			if err := writeMarkdown(filepath.Join(outDir, "report.md"), r); err != nil {
				return err
			}
		default:
			return fmt.Errorf("未知报告格式: %q", f)
		}
	}
	return nil
}

func writeJSON(path string, r *types.Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeMarkdown(path string, r *types.Report) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return renderMarkdown(f, r)
}
