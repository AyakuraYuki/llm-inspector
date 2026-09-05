package coord

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/reporter"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/runner"
	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// drainTimeout 是广播取消后等待各 agent 收敛的上限。RunLevel 收到取消后
// 只需等在途请求中止即可返回，正常几秒内完成；超过这个窗口说明 agent 异常。
const drainTimeout = 60 * time.Second

// levelTask 标识一个档位在某个 agent 上的任务分片。
type levelTask struct {
	client *Client
	taskID string
}

// pollOptions 控制一个档位的轮询与全局早停行为。
type pollOptions struct {
	interval     time.Duration
	agentTimeout time.Duration
	earlyStop    bool
	minSamples   int
	maxErrorRate float64
}

// agentPollState 是单 agent 的轮询状态：上一轮快照（算 delta 用）与健康时间戳。
type agentPollState struct {
	prevOK     int64
	prevByType map[types.ErrorType]int64
	latestN    int64
	latestFail int64
	lastOK     time.Time
	done       bool
}

// pollLevel 轮询该档位全部任务分片直到所有 agent 报告 Done。
//
//   - 每轮把各 agent 快照与上一轮做 delta，合成 rep.RequestDone 调用喂给
//     coordinator 的 TUI/Console（RequestDone 只做原子计数，合成开销可忽略）；
//   - 开启早停时按全局累计错误率判定（runner.ShouldStopEarly），触发即广播取消，
//     继续轮询等各 agent 排空；
//   - agent 连续 agentTimeout 无响应判失联，或 agent 上报任务异常，广播取消后报错；
//   - ctx 取消（用户中止）时广播取消，换用独立的排空 ctx 等 agent 收敛，
//     语义对齐单机版：中止档位仍返回已完成部分。
func pollLevel(ctx context.Context, tasks []levelTask, opt pollOptions, rep reporter.Reporter) (stopped bool, err error) {
	states := make([]agentPollState, len(tasks))
	now := time.Now()
	for i := range states {
		states[i].lastOK = now
	}

	reqTimeout := max(2*time.Second, opt.interval)
	pollCtx := ctx
	aborted := false

	ticker := time.NewTicker(opt.interval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			if !aborted {
				// 用户中止：广播取消后换独立排空 ctx，继续收敛
				aborted = true
				broadcastCancel(tasks)
				var cancelDrain context.CancelFunc
				pollCtx, cancelDrain = context.WithTimeout(context.Background(), drainTimeout)
				defer cancelDrain()
				continue
			}
			return stopped, fmt.Errorf("中止后等待 agent 排空超时（%s）", drainTimeout)
		case <-ticker.C:
		}

		var (
			mu       sync.Mutex
			failErrs []error
			wg       sync.WaitGroup
		)
		for i := range tasks {
			if states[i].done {
				continue
			}
			wg.Go(func() {
				reqCtx, cancel := context.WithTimeout(pollCtx, reqTimeout)
				defer cancel()
				p, pollErr := tasks[i].client.TaskProgress(reqCtx, tasks[i].taskID)

				mu.Lock()
				defer mu.Unlock()
				st := &states[i]
				if pollErr != nil {
					if time.Since(st.lastOK) > opt.agentTimeout {
						failErrs = append(failErrs, fmt.Errorf("agent %s 失联（连续 %s 无响应）: %w",
							tasks[i].client.Addr, opt.agentTimeout, pollErr))
					}
					return
				}
				st.lastOK = time.Now()
				st.latestN = p.N
				st.latestFail = p.N - p.OK

				emitDeltas(st, p.OK, p.ByType, p.ErrSamples, rep)

				if p.Err != "" {
					failErrs = append(failErrs, fmt.Errorf("agent %s 任务异常: %s", tasks[i].client.Addr, p.Err))
					return
				}
				if p.Done {
					st.done = true
				}
			})
		}
		wg.Wait()

		if len(failErrs) > 0 {
			broadcastCancel(tasks)
			return stopped, failErrs[0]
		}

		// 全局早停：本地判定关闭（下发的 EarlyStopEnabled 恒 false），
		// 由 coordinator 汇总所有 agent 的累计量统一决策
		if opt.earlyStop && !stopped {
			var sumN, sumFail int64
			for i := range states {
				sumN += states[i].latestN
				sumFail += states[i].latestFail
			}
			if runner.ShouldStopEarly(sumN, sumFail, opt.minSamples, opt.maxErrorRate) {
				stopped = true
				broadcastCancel(tasks)
			}
		}

		allDone := true
		for i := range states {
			if !states[i].done {
				allDone = false
				break
			}
		}
		if allDone {
			return stopped, nil
		}
	}
}

// emitDeltas 把与上一轮快照的增量合成 RequestDone 事件。
// 成功增量合成空成功指标；失败增量按错误类型合成，带上该类型的最近错误样本，
// 让 TUI 的失败原因面板拿到可读信息。
func emitDeltas(st *agentPollState, ok int64, byType map[types.ErrorType]int64, samples map[types.ErrorType]string, rep reporter.Reporter) {
	now := time.Now()
	for range ok - st.prevOK {
		rep.RequestDone(types.RequestMetrics{Timestamp: now, Success: true})
	}
	st.prevOK = ok

	if st.prevByType == nil {
		st.prevByType = make(map[types.ErrorType]int64)
	}
	for et, n := range byType {
		for range n - st.prevByType[et] {
			rep.RequestDone(types.RequestMetrics{Timestamp: now, Success: false, ErrorType: et, Error: samples[et]})
		}
		st.prevByType[et] = n
	}
}

// broadcastCancel 并行向所有分片广播取消，尽力而为（agent 可能已完成或失联）。
func broadcastCancel(tasks []levelTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, t := range tasks {
		wg.Go(func() { _ = t.client.TaskCancel(ctx, t.taskID) })
	}
	wg.Wait()
}
