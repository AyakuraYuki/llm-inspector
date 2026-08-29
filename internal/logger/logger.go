// Package logger 提供带时间戳的进度日志输出。
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	logfile = ""
)

func SetLogfile(path string) {
	mu.Lock()
	defer mu.Unlock()
	logfile = path
}

// SetLogfileForReportDir 按「报告目录名 + .txt」的约定，在当前工作目录下启用日志文件。
// reportDir 不必已经存在，此处只取它的目录名。
func SetLogfileForReportDir(reportDir string) {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	SetLogfile(filepath.Join(wd, filepath.Base(reportDir)+".txt"))
}

// Printf 输出带时间戳的日志，用于跟踪测试进度
func Printf(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format(time.DateTime), fmt.Sprintf(format, args...))

	mu.Lock()
	defer mu.Unlock()

	_, _ = fmt.Fprintln(os.Stdout, line)

	if logfile == "" {
		return
	}
	f, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logger: 无法打开日志文件 %s: %v\n", logfile, err)
		return
	}
	defer func() { _ = f.Close() }()
	// 去掉调用方自带的换行，保证一次调用只追加一行
	if _, err = fmt.Fprintln(f, strings.TrimRight(line, "\r\n")); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "logger: 写日志文件 %s 失败: %v\n", logfile, err)
	}
}
