// Package samplelog 把压测中每一条请求的原始指标落盘为 JSONL 文件，一行一条。
// 报表里的分位数是聚合结果，聚合口径一旦要改（换分位算法、换剔除阈值、按时段
// 切窗口重算），没有原始样本就只能重跑一遍压测——而压测环境往往不可复现。
// 一次落盘换来任意事后重算，也是后续统计类迭代（稳态窗口、SLA 搜索）的地基。
//
// 使用方式与 internal/errlog 一致：进程启动时 Init 指定输出文件，未 Init 时
// 所有写入都是空操作；文件在首次写入时才创建，未导出时不留空文件。
// 每个档位结束后调用 WriteLevel 落盘该档位的全部样本（含失败样本），
// 收尾调用 Close 冲刷缓冲。
package samplelog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// LevelContext 是一个档位的标识信息。逐样本重复写入（而非分组嵌套）是刻意的：
// JSONL 每行自足才能用 jq/pandas 直接过滤，不必先解析层级结构。
type LevelContext struct {
	Model       string
	Provider    types.Provider
	TokenGroup  string
	Concurrency int
	TargetRate  float64 // open-loop 档位的目标 RPS，closed-loop 为 0（省略）
	LevelStart  time.Time
}

// sampleLine 是一条样本记录，序列化为 JSONL 中的一行。
// 字段名与 report 的 JSON/CSV 报告保持同一套命名（*_ms、*_tokens），
// 便于把样本级数据和聚合报告放在一起分析。
type sampleLine struct {
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	TokenGroup  string  `json:"token_group,omitempty"`
	Concurrency int     `json:"concurrency"`
	TargetRate  float64 `json:"target_rate_rps,omitempty"`
	LevelStart  string  `json:"level_start"`

	Timestamp string  `json:"ts"` // 请求发起时刻
	Success   bool    `json:"success"`
	TTFTMs    float64 `json:"ttft_ms,omitempty"`
	E2EMs     float64 `json:"e2e_ms"`

	InputTokens       int64 `json:"input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
	OutputEstimated   bool  `json:"output_estimated,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	CacheReported     bool  `json:"cache_reported,omitempty"`

	// usage 对拍（配置了本地分词器时才有值）：local_output_tokens 为 0 表示该请求未对拍
	LocalOutputTokens int64   `json:"local_output_tokens,omitempty"`
	UsageDriftPct     float64 `json:"usage_drift_pct,omitempty"`
	UsageDrifted      bool    `json:"usage_drifted,omitempty"`

	// ITLMs 是逐输出内容事件的间隔（毫秒）。这是全文件体积的主要来源
	//（每条成功样本一个数组，长度约等于输出事件数），但没有它就无法事后
	// 重算 ITL 分布，而 ITL 恰恰是解码卡顿最灵敏的指标。
	ITLMs []float64 `json:"itl_ms,omitempty"`

	ErrorType string `json:"error_type,omitempty"`
	Error     string `json:"error,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

var (
	mu    sync.Mutex
	path  string
	file  *os.File
	buf   *bufio.Writer
	count int64
)

// Init 指定样本文件路径并重置计数。传入空字符串等价于关闭导出。
func Init(p string) {
	mu.Lock()
	defer mu.Unlock()
	closeLocked()
	path = p
	count = 0
}

// Enabled 报告本次运行是否开启了样本导出。
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return path != ""
}

// Path 返回 Init 设置的输出文件路径。
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Count 返回已写入的样本条数。
func Count() int64 {
	mu.Lock()
	defer mu.Unlock()
	return count
}

// WriteLevel 追加一个档位的全部样本并冲刷缓冲。未 Init 时为空操作。
// 预热档位不应调用（预热样本按设计不进入任何统计）。
func WriteLevel(lv LevelContext, samples []types.RequestMetrics) {
	if len(samples) == 0 {
		return
	}

	mu.Lock()
	defer mu.Unlock()
	if path == "" {
		return
	}
	if !openLocked() {
		return
	}

	levelStart := ""
	if !lv.LevelStart.IsZero() {
		levelStart = lv.LevelStart.Format(time.RFC3339Nano)
	}
	enc := json.NewEncoder(buf)
	for _, m := range samples {
		line := sampleLine{
			Model:             lv.Model,
			Provider:          string(lv.Provider),
			TokenGroup:        lv.TokenGroup,
			Concurrency:       lv.Concurrency,
			TargetRate:        lv.TargetRate,
			LevelStart:        levelStart,
			Success:           m.Success,
			TTFTMs:            msOf(m.TTFT),
			E2EMs:             msOf(m.TotalLatency),
			InputTokens:       m.InputTokens,
			OutputTokens:      m.OutputTokens,
			OutputEstimated:   m.OutputEstimated,
			CachedInputTokens: m.CachedInputTokens,
			CacheReported:     m.CacheReported,
			LocalOutputTokens: m.LocalOutputTokens,
			UsageDriftPct:     m.UsageDriftPct,
			UsageDrifted:      m.UsageDrifted,
			ITLMs:             m.ITLSamplesMS,
			ErrorType:         string(m.ErrorType),
			Error:             m.Error,
			RequestID:         m.RequestID,
		}
		if !m.Timestamp.IsZero() {
			line.Timestamp = m.Timestamp.Format(time.RFC3339Nano)
		}
		if err := enc.Encode(line); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "samplelog: 序列化样本失败: %v\n", err)
			return
		}
		count++
	}
	if err := buf.Flush(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "samplelog: 写样本文件 %s 失败: %v\n", path, err)
	}
}

// Close 冲刷缓冲并关闭文件，可重复调用。
func Close() {
	mu.Lock()
	defer mu.Unlock()
	closeLocked()
}

// openLocked 惰性创建目录与文件，返回是否可写。调用方须持有 mu。
func openLocked() bool {
	if file != nil {
		return true
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "samplelog: 无法创建目录 %s: %v\n", dir, err)
			return false
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "samplelog: 无法打开样本文件 %s: %v\n", path, err)
		return false
	}
	file = f
	buf = bufio.NewWriterSize(f, 256*1024)
	return true
}

// closeLocked 冲刷并释放文件句柄。调用方须持有 mu。
func closeLocked() {
	if buf != nil {
		_ = buf.Flush()
		buf = nil
	}
	if file != nil {
		_ = file.Close()
		file = nil
	}
}

// msOf 把 Duration 转为毫秒；非正值返回 0（JSON 里被 omitempty 省略）。
func msOf(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d) / float64(time.Millisecond)
}
