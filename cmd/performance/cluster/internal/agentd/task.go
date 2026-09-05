package agentd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/cluster/internal/proto"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// watchdogTimeout 是孤儿任务的自我取消窗口：coordinator 每秒轮询 progress，
// 运行中任务这么久收不到任何 progress 请求，说明 coordinator 已消失
// （进程被杀/网络分区），继续空压上游没有意义，主动取消止损。
const watchdogTimeout = 60 * time.Second

// runningTask 是 agent 上一个档位任务的完整生命周期状态。
// agent 单任务互斥：同一时刻至多一个 runningTask，done 之后结果缓存在
// result 里等 coordinator 拉取，下一个 task/start 直接替换。
type runningTask struct {
	id       string
	kind     proto.TaskKind
	cancel   context.CancelFunc
	rep      *counterReporter
	start    time.Time
	lastPoll atomic.Int64 // UnixNano，progress 请求刷新，watchdog 检查
	done     atomic.Bool

	mu     sync.Mutex // 保护 result/runErr（写在任务协程，读在 handler）
	result types.BenchmarkResult
	runErr error
}

// newRunningTask 启动任务协程与看门狗，立即返回。
func newRunningTask(req proto.TaskStart) *runningTask {
	ctx, cancel := context.WithCancel(context.Background())
	t := &runningTask{
		id:     req.TaskID,
		kind:   req.Kind,
		cancel: cancel,
		rep:    &counterReporter{},
		start:  time.Now(),
	}
	t.lastPoll.Store(t.start.UnixNano())

	go t.run(ctx, req)
	go t.watchdog(ctx)
	return t
}

// run 执行档位任务：RunLevel 阻塞至 deadline/取消，结果缓存等待拉取。
// panic 恢复后记入 runErr，通过 progress 的 Err 字段回传给 coordinator。
func (t *runningTask) run(ctx context.Context, req proto.TaskStart) {
	defer t.cancel()
	defer func() {
		if r := recover(); r != nil {
			t.mu.Lock()
			t.runErr = fmt.Errorf("agent task panic: %v", r)
			t.mu.Unlock()
		}
		t.done.Store(true)
	}()

	result := runner.RunLevel(ctx, req.Bench, req.Model, req.Concurrency, req.Ramp, t.rep)
	if req.Kind == proto.TaskWarmup {
		result.Metrics = nil // 预热结果丢弃，不占回传带宽
	}

	t.mu.Lock()
	t.result = result
	t.mu.Unlock()
}

// watchdog 周期检查 coordinator 是否还在轮询，失联即取消任务。
func (t *runningTask) watchdog(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if t.done.Load() {
				return
			}
			if time.Since(time.Unix(0, t.lastPoll.Load())) > watchdogTimeout {
				t.cancel()
				return
			}
		}
	}
}

// progress 组装当前任务的进度快照，并刷新看门狗时间戳。
func (t *runningTask) progress() proto.TaskProgress {
	t.lastPoll.Store(time.Now().UnixNano())

	n, ok, byType := t.rep.counters.Snapshot()
	p := proto.TaskProgress{
		TaskID:     t.id,
		Done:       t.done.Load(),
		N:          n,
		OK:         ok,
		ByType:     byType,
		ErrSamples: t.rep.samples.snapshot(),
		Elapsed:    time.Since(t.start),
	}
	if p.Done {
		t.mu.Lock()
		if t.runErr != nil {
			p.Err = t.runErr.Error()
		}
		t.mu.Unlock()
	}
	return p
}

// takeResult 返回已完成任务的结果。任务未完成时返回 false。
func (t *runningTask) takeResult() (types.BenchmarkResult, error, bool) {
	if !t.done.Load() {
		return types.BenchmarkResult{}, nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.result, t.runErr, true
}
