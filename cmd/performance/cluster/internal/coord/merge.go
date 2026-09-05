package coord

import (
	"sort"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// RebaseResult 把 agent 本机时间轴平移到 coordinator 的 t0：
// Timestamp' = t0 + (Timestamp − res.Start)。同一台机器的两次 wall-clock
// 读数相减近似单调时长，跨机时钟偏斜在减法中被消去，因此合并后的样本
// 在统一时间轴上可比，metrics.AggregateMetrics 的吞吐窗口判定
// （cutoff = Start + window）对 rebase 后的样本天然成立。
func RebaseResult(res types.BenchmarkResult, t0 time.Time) types.BenchmarkResult {
	for i := range res.Metrics {
		res.Metrics[i].Timestamp = t0.Add(res.Metrics[i].Timestamp.Sub(res.Start))
	}
	res.Start = t0
	return res
}

// MergeLevel 把同一档位各 agent 的分片结果合并为一个全局 BenchmarkResult：
// 原始样本取并集（按 Timestamp 排序，错误明细 sheet 依赖时间序），
// Elapsed 取各分片最大值（档位实际时长由最慢的节点决定），
// Concurrency 记全局并发。parts 必须已经过 RebaseResult 归一。
func MergeLevel(t0 time.Time, window time.Duration, global int, stopped bool, parts []types.BenchmarkResult) types.BenchmarkResult {
	merged := types.BenchmarkResult{
		Concurrency:  global,
		Start:        t0,
		Window:       window,
		StoppedEarly: stopped,
	}
	total := 0
	for _, p := range parts {
		total += len(p.Metrics)
	}
	merged.Metrics = make([]types.RequestMetrics, 0, total)
	for _, p := range parts {
		if merged.Model == "" {
			merged.Model = p.Model
			merged.Provider = p.Provider
			merged.TokenGroup = p.TokenGroup
		}
		if p.Elapsed > merged.Elapsed {
			merged.Elapsed = p.Elapsed
		}
		merged.Metrics = append(merged.Metrics, p.Metrics...)
	}
	sort.Slice(merged.Metrics, func(i, j int) bool {
		return merged.Metrics[i].Timestamp.Before(merged.Metrics[j].Timestamp)
	})
	return merged
}
