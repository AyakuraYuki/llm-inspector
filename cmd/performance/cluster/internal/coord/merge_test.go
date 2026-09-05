package coord

import (
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/metrics"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// sample 构造一条成功样本：offset 是相对该 agent 本机 Start 的发起偏移。
func sample(start time.Time, offset, latency time.Duration) types.RequestMetrics {
	return types.RequestMetrics{
		Timestamp:    start.Add(offset),
		TotalLatency: latency,
		TTFT:         latency / 10,
		OutputTokens: 100,
		Success:      true,
	}
}

// TestRebaseResultClockSkew 验证时钟归一：两台 agent 的 wall-clock 相差 5 分钟，
// 但样本相对各自 Start 的偏移相同，rebase 后应落在统一时间轴的同一位置。
func TestRebaseResultClockSkew(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	startA := t0.Add(50 * time.Millisecond) // agent A 时钟基本对准
	startB := t0.Add(-5 * time.Minute)      // agent B 时钟慢 5 分钟
	offsets := []time.Duration{0, time.Second, 3 * time.Second}

	build := func(start time.Time) types.BenchmarkResult {
		res := types.BenchmarkResult{Start: start}
		for _, off := range offsets {
			res.Metrics = append(res.Metrics, sample(start, off, 500*time.Millisecond))
		}
		return res
	}

	ra := RebaseResult(build(startA), t0)
	rb := RebaseResult(build(startB), t0)

	if !ra.Start.Equal(t0) || !rb.Start.Equal(t0) {
		t.Fatalf("rebase 后 Start 应为 t0，got A=%v B=%v", ra.Start, rb.Start)
	}
	for i, off := range offsets {
		want := t0.Add(off)
		if !ra.Metrics[i].Timestamp.Equal(want) {
			t.Errorf("agent A 样本 %d 时间戳 = %v, want %v", i, ra.Metrics[i].Timestamp, want)
		}
		if !rb.Metrics[i].Timestamp.Equal(want) {
			t.Errorf("agent B（时钟偏斜 5 分钟）样本 %d 时间戳 = %v, want %v", i, rb.Metrics[i].Timestamp, want)
		}
	}
}

func TestMergeLevel(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	window := 10 * time.Second

	partA := types.BenchmarkResult{
		Model: "m1", Provider: types.ProviderOpenAI, TokenGroup: "g1",
		Start: t0, Elapsed: 11 * time.Second,
		Metrics: []types.RequestMetrics{
			sample(t0, 2*time.Second, time.Second),
			sample(t0, 0, time.Second),
		},
	}
	partB := types.BenchmarkResult{
		Model: "m1", Provider: types.ProviderOpenAI, TokenGroup: "g1",
		Start: t0, Elapsed: 12 * time.Second,
		Metrics: []types.RequestMetrics{
			sample(t0, time.Second, time.Second),
		},
	}

	merged := MergeLevel(t0, window, 100, true, []types.BenchmarkResult{partA, partB})

	if merged.Concurrency != 100 {
		t.Errorf("Concurrency = %d, want 100（全局并发）", merged.Concurrency)
	}
	if merged.Elapsed != 12*time.Second {
		t.Errorf("Elapsed = %v, want 12s（各分片最大值）", merged.Elapsed)
	}
	if !merged.StoppedEarly {
		t.Error("StoppedEarly 未透传")
	}
	if merged.Window != window {
		t.Errorf("Window = %v, want %v", merged.Window, window)
	}
	if len(merged.Metrics) != 3 {
		t.Fatalf("样本数 = %d, want 3", len(merged.Metrics))
	}
	for i := 1; i < len(merged.Metrics); i++ {
		if merged.Metrics[i].Timestamp.Before(merged.Metrics[i-1].Timestamp) {
			t.Errorf("样本未按 Timestamp 排序：[%d]=%v > [%d]=%v",
				i-1, merged.Metrics[i-1].Timestamp, i, merged.Metrics[i].Timestamp)
		}
	}
	if merged.Model != "m1" || merged.Provider != types.ProviderOpenAI || merged.TokenGroup != "g1" {
		t.Errorf("模型标识未从分片继承：%+v", merged)
	}
}

// TestMergeThenAggregateWindowCutoff 验证 rebase+merge 后的结果经
// metrics.AggregateMetrics 聚合时，吞吐窗口的 cutoff 判定语义正确：
// 窗口内完成的样本计入 QPS 分母，deadline 前发出、窗口外才完成的长尾
// 样本计入时延分位数但不计入 QPS。
func TestMergeThenAggregateWindowCutoff(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	window := 10 * time.Second

	// agent 时钟快 3 分钟：本机 Start 与样本一起被 rebase 归一
	agentStart := t0.Add(3 * time.Minute)
	part := types.BenchmarkResult{
		Model: "m1", Provider: types.ProviderOpenAI,
		Start: agentStart, Elapsed: 14 * time.Second,
		Metrics: []types.RequestMetrics{
			sample(agentStart, time.Second, time.Second),     // 2s 完成，窗口内
			sample(agentStart, 9*time.Second, 5*time.Second), // 14s 完成，窗口外长尾
		},
	}

	merged := MergeLevel(t0, window, 10, false, []types.BenchmarkResult{RebaseResult(part, t0)})
	agg := metrics.AggregateMetrics(merged)

	if agg.Success != 2 {
		t.Fatalf("Success = %d, want 2", agg.Success)
	}
	if agg.Latency.N != 2 {
		t.Errorf("时延样本数 = %d, want 2（长尾样本应计入时延分位数）", agg.Latency.N)
	}
	wantQPS := 1.0 / window.Seconds() // 只有 1 条在窗口内完成
	if diff := agg.QPS - wantQPS; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("QPS = %v, want %v（窗口外长尾不计入分母）", agg.QPS, wantQPS)
	}
}
