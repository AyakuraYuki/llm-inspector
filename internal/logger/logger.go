// Package logger 提供带时间戳的进度日志输出。
package logger

import (
	"fmt"
	"time"
)

// Printf 输出带时间戳的日志，用于跟踪测试进度
func Printf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
