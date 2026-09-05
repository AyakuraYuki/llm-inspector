package coord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/agentd"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// nopReporter 满足 reporter.Reporter，测试里丢弃全部事件。
type nopReporter struct{}

func (nopReporter) PreflightStart(int)                                    {}
func (nopReporter) PreflightResult(types.ModelSpec, types.RequestMetrics) {}
func (nopReporter) PreflightEnd(bool)                                     {}
func (nopReporter) WarmupStart(int, time.Duration)                        {}
func (nopReporter) WarmupModel(int, int, types.ModelSpec, time.Time)      {}
func (nopReporter) WarmupEnd()                                            {}
func (nopReporter) LevelStart(int, int, types.ModelSpec, int, time.Time)  {}
func (nopReporter) RequestDone(types.RequestMetrics)                      {}
func (nopReporter) LevelEnd(types.AggregatedMetrics)                      {}
func (nopReporter) EarlyStop(types.ModelSpec, int, float64)               {}
func (nopReporter) CooldownStart(time.Duration)                           {}
func (nopReporter) BenchmarkEnd(bool)                                     {}

// sseStub 返回一个最小可用的 OpenAI SSE 上游：内容增量 + usage + [DONE]。
// okBudget >= 0 时，前 okBudget 个请求成功、其后全部 500（用于早停路径）；
// okBudget < 0 时全部成功。
func sseStub(okBudget int64) http.HandlerFunc {
	var served atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		if n := served.Add(1); okBudget >= 0 && n > okBudget {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fl.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}
}

// startCluster 起一个 stub 上游和两个进程内 agent，返回压测配置。
func startCluster(t *testing.T, upstream http.HandlerFunc) (types.BenchmarkConfig, *config.ClusterConfig) {
	t.Helper()
	t.Chdir(t.TempDir()) // agent 的 errlog 落在临时目录，不污染仓库

	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	a1 := httptest.NewServer(agentd.NewServer("").Handler())
	t.Cleanup(a1.Close)
	a2 := httptest.NewServer(agentd.NewServer("").Handler())
	t.Cleanup(a2.Close)

	bench := types.BenchmarkConfig{
		BaseURL: up.URL,
		Models: []types.ModelSpec{{
			Name: "stub-model", Provider: types.ProviderOpenAI,
			TokenGroup: "default", Tokens: []string{"sk-test"},
		}},
		Concurrency: []int{4},
		Duration:    2 * time.Second,
		Prompt:      "hi",
	}
	cluster := &config.ClusterConfig{
		Agents: []string{
			strings.TrimPrefix(a1.URL, "http://"),
			strings.TrimPrefix(a2.URL, "http://"),
		},
		PollInterval: 100 * time.Millisecond,
		AgentTimeout: 5 * time.Second,
	}
	return bench, cluster
}

// TestClusterIntegration 驱动完整流程：ping → session → preflight →
// 档位（双 agent 分片）→ 结果回收合并聚合。err == nil 本身即证明
// 两个 agent 都完成了任务且结果都收回来了（pollLevel/collect 缺一即报错）。
func TestClusterIntegration(t *testing.T) {
	bench, cluster := startCluster(t, sseStub(-1))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results, summary, err := RunBenchmark(ctx, bench, cluster, nopReporter{})
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果档位数 = %d, want 1", len(results))
	}

	agg := results[0]
	if agg.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4（全局并发）", agg.Concurrency)
	}
	if agg.Total < 2 {
		t.Errorf("Total = %d, want >= 2（双 agent 各至少一条样本）", agg.Total)
	}
	if agg.Failed != 0 {
		t.Errorf("Failed = %d, want 0", agg.Failed)
	}
	if agg.QPS <= 0 {
		t.Errorf("QPS = %v, want > 0", agg.QPS)
	}
	if agg.StoppedEarly {
		t.Error("StoppedEarly 不应为 true")
	}

	if len(summary.Agents) != 2 {
		t.Fatalf("summary agent 数 = %d, want 2", len(summary.Agents))
	}
	for _, a := range summary.Agents {
		if a.MaxShare != 2 {
			t.Errorf("agent %s MaxShare = %d, want 2（4 并发 ÷ 2 节点）", a.Addr, a.MaxShare)
		}
	}
}

// TestClusterEarlyStopBroadcast 验证全局早停：preflight 的 2 个请求成功后
// 上游全部 500，coordinator 汇总全局错误率触发早停并广播取消，
// 档位远早于 30s 的名义时长结束且标记 StoppedEarly。
func TestClusterEarlyStopBroadcast(t *testing.T) {
	bench, cluster := startCluster(t, sseStub(2)) // 预算 2 = 双 agent 的 preflight
	bench.Duration = 30 * time.Second
	bench.EarlyStopEnabled = true
	bench.MaxErrorRate = 0.5
	bench.MinSamples = 4
	bench.SkipHigherConcurrency = true

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	start := time.Now()
	results, _, err := RunBenchmark(ctx, bench, cluster, nopReporter{})
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("结果档位数 = %d, want 1", len(results))
	}
	if !results[0].StoppedEarly {
		t.Error("StoppedEarly 未标记")
	}
	if results[0].Failed == 0 {
		t.Error("应有失败样本")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("早停耗时 %v，未在名义时长（30s）前明显收敛", elapsed)
	}
}
