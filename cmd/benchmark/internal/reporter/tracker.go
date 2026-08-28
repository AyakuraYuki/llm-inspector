package reporter

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyakuraYuki/llm-inspector/internal/logger"
)

// ProgressTracker 跟踪 benchmark 的执行进度，用于监控当前正在执行的测试项目
type ProgressTracker struct {
	total      int
	startTime  time.Time
	completed  atomic.Int64      // 原子计数：已完成的问题数
	mu         sync.Mutex        // 保护 inProgress
	inProgress map[int]time.Time // 问题索引（0-based） -> 开始执行的时间
}

func NewProgressTracker(total int) *ProgressTracker {
	return &ProgressTracker{
		total:      total,
		startTime:  time.Now(),
		inProgress: make(map[int]time.Time),
	}
}

// Start 标记某个问题开始执行
func (p *ProgressTracker) Start(index int) {
	p.mu.Lock()
	p.inProgress[index] = time.Now()
	p.mu.Unlock()
	logger.Printf("Question %d started", index+1)
}

// Finish 标记某个问题执行完成，返回累计已完成的问题数
func (p *ProgressTracker) Finish(index int) int {
	p.mu.Lock()
	delete(p.inProgress, index)
	p.mu.Unlock()
	return int(p.completed.Add(1))
}

// Report 输出整体进度以及当前正在执行的测试项目及其已运行时长
func (p *ProgressTracker) Report() {
	completed := p.completed.Load()

	p.mu.Lock()
	running := make([]int, 0, len(p.inProgress))
	starts := make(map[int]time.Time, len(p.inProgress))
	for idx, t := range p.inProgress {
		running = append(running, idx)
		starts[idx] = t
	}
	p.mu.Unlock()
	sort.Ints(running)

	elapsed := time.Since(p.startTime).Round(time.Second)
	logger.Printf("Progress: %d/%d completed, %d in progress (elapsed %s)", completed, p.total, len(running), elapsed)
	for _, idx := range running {
		logger.Printf("  -> question %d running for %s", idx+1, time.Since(starts[idx]).Round(time.Second))
	}
}

// Monitor 定期输出进度，直到 ctx 被取消
func (p *ProgressTracker) Monitor(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Report()
		}
	}
}
