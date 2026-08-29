package reporter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

const (
	maxFailSamples     = 8               // 失败抽样面板保留的最大条数
	maxLogLines        = 6               // 已完成事件日志保留的最大条数
	failSampleInterval = 1 * time.Second // 同一错误类型的最小抽样间隔
	failSampleMaxLen   = 400             // 单条失败样本保留的最大字符数
)

// tuiPhase 标识压测当前所处阶段
type tuiPhase int

const (
	phasePreflight tuiPhase = iota
	phaseWarmup
	phaseRunning
	phaseCooldown
	phaseFinished
)

// failSample 保存一条失败请求的抽样记录
type failSample struct {
	at    time.Time
	model string
	et    types.ErrorType
	msg   string
}

// TUIState 保存 TUI 渲染所需的全部状态。
// 热路径计数走 counters 的原子操作；其余低频字段由 mu 保护，
// 渲染协程每帧加锁做一次快照。
type TUIState struct {
	counters     LevelCounters
	mu           sync.Mutex
	phase        tuiPhase
	seq, total   int
	model        types.ModelSpec
	concurrency  int
	phaseStart   time.Time
	deadline     time.Time
	log          []string
	samples      []failSample
	lastSampleAt map[types.ErrorType]time.Time
	cumRequests  int64 // 已完成档位的累计请求数（不含当前档位）
	cumFailed    int64
	aborting     bool
	benchStart   time.Time
}

func NewTuiState() *TUIState {
	return &TUIState{
		lastSampleAt: make(map[types.ErrorType]time.Time),
		benchStart:   time.Now(),
	}
}

func (s *TUIState) AppendLog(line string) {
	s.log = append(s.log, line)
	if len(s.log) > maxLogLines {
		s.log = s.log[len(s.log)-maxLogLines:]
	}
}
func (s *TUIState) Lock()           { s.mu.Lock() }
func (s *TUIState) Unlock()         { s.mu.Unlock() }
func (s *TUIState) IsAborted() bool { return s.aborting }

// TUIReporter 将 Runner 事件写入 tuiState；渲染由 bubbletea 的定时 tick 驱动。
type TUIReporter struct {
	State *TUIState
	Prog  *tea.Program
}

var _ Reporter = (*TUIReporter)(nil)

func (r *TUIReporter) PreflightStart(int) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phasePreflight
	s.phaseStart = time.Now()
}

func (r *TUIReporter) PreflightResult(model types.ModelSpec, m types.RequestMetrics) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.Success {
		s.AppendLog(fmt.Sprintf("[预检 OK] %s (%s, group=%s) %.0fms",
			model.Name, model.Provider, model.TokenGroup, float64(m.TotalLatency)/float64(time.Millisecond)))
	} else {
		s.AppendLog(fmt.Sprintf("[预检 FAIL] %s (%s, group=%s) %s",
			model.Name, model.Provider, model.TokenGroup, oneLine(m.Error, 120)))
	}
}

func (r *TUIReporter) PreflightEnd(bool) {}

func (r *TUIReporter) WarmupStart(concurrency int, duration time.Duration) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phaseWarmup
	s.concurrency = concurrency
}

func (r *TUIReporter) WarmupModel(idx, total int, model types.ModelSpec, deadline time.Time) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq, s.total = idx, total
	s.model = model
	s.phaseStart = time.Now()
	s.deadline = deadline
	s.counters.Reset()
}

func (r *TUIReporter) WarmupEnd() {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AppendLog("[预热] 完成")
}

func (r *TUIReporter) LevelStart(seq, total int, model types.ModelSpec, concurrency int, deadline time.Time) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phaseRunning
	s.seq, s.total = seq, total
	s.model = model
	s.concurrency = concurrency
	s.phaseStart = time.Now()
	s.deadline = deadline
	s.counters.Reset()
}

func (r *TUIReporter) RequestDone(m types.RequestMetrics) {
	r.State.counters.Add(m)
	if m.Success {
		return
	}

	// 失败抽样：同一错误类型限速记录，避免高失败率时刷屏和锁竞争
	s := r.State
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastSampleAt[m.ErrorType]) < failSampleInterval {
		return
	}
	s.lastSampleAt[m.ErrorType] = now
	s.samples = append(s.samples, failSample{
		at:    now,
		model: s.model.Name,
		et:    m.ErrorType,
		msg:   oneLine(m.Error, failSampleMaxLen),
	})
	if len(s.samples) > maxFailSamples {
		s.samples = s.samples[len(s.samples)-maxFailSamples:]
	}
}

func (r *TUIReporter) LevelEnd(agg types.AggregatedMetrics) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cumRequests += int64(agg.Total)
	s.cumFailed += int64(agg.Failed)
	errPct := 0.0
	if agg.Total > 0 {
		errPct = float64(agg.Failed) / float64(agg.Total) * 100
	}
	s.AppendLog(fmt.Sprintf("[%d/%d] %s [%s] c=%d done: %d req, %d ok, %d failed (%.1f%%)",
		s.seq, s.total, agg.Model, agg.TokenGroup, agg.Concurrency, agg.Total, agg.Success, agg.Failed, errPct))
}

func (r *TUIReporter) CooldownStart(d time.Duration) {
	s := r.State
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phaseCooldown
	s.phaseStart = time.Now()
	s.deadline = time.Now().Add(d)
}

func (r *TUIReporter) BenchmarkEnd(bool) {
	s := r.State
	s.mu.Lock()
	s.phase = phaseFinished
	s.mu.Unlock()
	r.Prog.Send(BenchDoneMsg{})
}

// ---- bubbletea ----

type tickMsg time.Time

type BenchDoneMsg struct{}

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleFaint    = lipgloss.NewStyle().Faint(true)
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleFail     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleBar      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	stylePanel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	styleErrLabel = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
)

// tuiModel 是 bubbletea 的 Model，实际状态都在 state 里，
// 渲染协程每 200ms 取一次快照重绘。
type tuiModel struct {
	state  *TUIState
	cancel context.CancelFunc
	cfg    types.BenchmarkConfig
	width  int
}

func NewTuiModel(state *TUIState, cancel context.CancelFunc, cfg types.BenchmarkConfig) tuiModel {
	return tuiModel{state: state, cancel: cancel, cfg: cfg, width: 100}
}

func tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m tuiModel) Init() tea.Cmd {
	return tick()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		return m, tick()
	case BenchDoneMsg:
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.InterruptMsg:
		// 外部 SIGINT（如 kill -INT）：与按 q 一样优雅中止
		return m.abort()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m.abort()
		}
	}
	return m, nil
}

// abort 触发优雅中止；重复触发时不再等待在途请求，直接退出 TUI。
func (m tuiModel) abort() (tea.Model, tea.Cmd) {
	s := m.state
	s.mu.Lock()
	already := s.aborting
	s.aborting = true
	s.mu.Unlock()
	m.cancel()
	if already {
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) View() string {
	s := m.state
	n, ok, byType := s.counters.Snapshot()

	s.mu.Lock()
	phase := s.phase
	seq, total := s.seq, s.total
	model := s.model
	conc := s.concurrency
	phaseStart := s.phaseStart
	deadline := s.deadline
	logLines := append([]string(nil), s.log...)
	samples := append([]failSample(nil), s.samples...)
	cumReq, cumFail := s.cumRequests, s.cumFailed
	aborting := s.aborting
	benchStart := s.benchStart
	s.mu.Unlock()

	width := max(min(m.width, 120), 60)
	inner := width - 4 // 面板边框+内边距占 4 列

	var sections []string

	// 标题行
	elapsed := time.Since(benchStart).Round(time.Second)
	sections = append(sections, styleTitle.Render("NewAPI Benchmark")+
		styleFaint.Render(fmt.Sprintf("  %s  已运行 %s", m.cfg.BaseURL, elapsed)))

	// 阶段面板
	var phaseLines []string
	switch phase {
	case phasePreflight:
		phaseLines = append(phaseLines, styleWarn.Render("● 预检")+"  逐个模型验证连通性...")
	case phaseWarmup:
		phaseLines = append(phaseLines,
			styleWarn.Render("● 预热")+fmt.Sprintf("  [%d/%d] %s (%s)  group=%s  并发=%d", seq, total, model.Name, model.Provider, model.TokenGroup, conc),
			progressLine(phaseStart, deadline, inner))
	case phaseRunning:
		phaseLines = append(phaseLines,
			styleOK.Render("● 压测")+fmt.Sprintf("  [%d/%d] %s (%s)  group=%s  并发=%d", seq, total, model.Name, model.Provider, model.TokenGroup, conc),
			progressLine(phaseStart, deadline, inner))
		if total > 0 {
			levelFrac := clamp01(time.Since(phaseStart).Seconds() / max(deadline.Sub(phaseStart).Seconds(), 0.001))
			overall := (float64(seq-1) + levelFrac) / float64(total)
			prefix, suffix := "总进度 ", fmt.Sprintf(" %d/%d 档位", seq, total)
			barWidth := inner - lipgloss.Width(prefix) - lipgloss.Width(suffix)
			phaseLines = append(phaseLines, prefix+renderBar(overall, barWidth)+suffix)
		}
	case phaseCooldown:
		phaseLines = append(phaseLines, styleFaint.Render("● 冷却")+
			fmt.Sprintf("  %.0fs 后进入下一并发档位", max(time.Until(deadline).Seconds(), 0)))
	case phaseFinished:
		phaseLines = append(phaseLines, styleOK.Render("● 完成")+"  正在生成报告...")
	}
	sections = append(sections, stylePanel.Width(width-2).Render(strings.Join(phaseLines, "\n")))

	// 统计面板（预热/压测阶段展示当前档位实时计数）
	if phase == phaseWarmup || phase == phaseRunning {
		failed := n - ok
		stats := fmt.Sprintf("请求 %s   成功 %s   失败 %s",
			styleTitle.Render(fmt.Sprintf("%d", n)),
			styleOK.Render(fmt.Sprintf("%d", ok)),
			failStyle(failed).Render(fmt.Sprintf("%d (%.1f%%)", failed, pct(failed, n))))
		stats += "\n" + styleFaint.Render(formatErrorCounts(byType))
		if cumReq > 0 {
			stats += styleFaint.Render(fmt.Sprintf("   |  累计(已完成档位) %d req, %d failed", cumReq, cumFail))
		}
		sections = append(sections, stylePanel.Width(width-2).Render(stats))
	}

	// 事件日志面板
	if len(logLines) > 0 {
		lg := []string{styleFaint.Render("最近事件")}
		for _, line := range logLines {
			lg = append(lg, truncLine(line, inner))
		}
		sections = append(sections, stylePanel.Width(width-2).Render(strings.Join(lg, "\n")))
	}

	// 失败抽样面板
	fp := []string{styleErrLabel.Render("失败抽样") +
		styleFaint.Render(fmt.Sprintf("（每种错误类型至多 %s 一条，保留最近 %d 条）", failSampleInterval, maxFailSamples))}
	if len(samples) == 0 {
		fp = append(fp, styleFaint.Render("（暂无失败请求）"))
	} else {
		for _, sm := range samples {
			// 截断预算按可见字符计算（ANSI 颜色码不占宽度），响应内容最多占两行
			prefix := fmt.Sprintf("%s [%s] %s ", sm.at.Format("15:04:05"), sm.et, sm.model)
			budget := max(inner*2-len([]rune(prefix)), 20)
			fp = append(fp, fmt.Sprintf("%s %s %s %s",
				styleFaint.Render(sm.at.Format("15:04:05")),
				styleFail.Render(fmt.Sprintf("[%s]", sm.et)),
				styleFaint.Render(sm.model),
				truncLine(sm.msg, budget)))
		}
	}
	sections = append(sections, stylePanel.Width(width-2).Render(strings.Join(fp, "\n")))

	// 底部提示
	if aborting {
		sections = append(sections, styleWarn.Render("正在中止，等待在途请求结束...（再按一次 q 立即退出）"))
	} else {
		sections = append(sections, styleFaint.Render("q / ctrl+c 中止压测并输出已完成部分的报告"))
	}

	return strings.Join(sections, "\n") + "\n"
}

// progressLine 渲染 "进度条 已用/总时长 剩余" 一行。
func progressLine(start, deadline time.Time, width int) string {
	totalDur := deadline.Sub(start)
	elapsed := time.Since(start)
	frac := clamp01(elapsed.Seconds() / max(totalDur.Seconds(), 0.001))
	remain := max(time.Until(deadline).Round(time.Second), 0)
	label := fmt.Sprintf(" %s/%s 剩余 %s", elapsed.Round(time.Second), totalDur.Round(time.Second), remain)
	return renderBar(frac, width-lipgloss.Width(label)) + label
}

// renderBar 渲染指定宽度的进度条。
func renderBar(frac float64, width int) string {
	width = max(width, 10)
	filled := int(clamp01(frac) * float64(width))
	return styleBar.Render(strings.Repeat("█", filled)) + styleFaint.Render(strings.Repeat("░", width-filled))
}

func clamp01(f float64) float64 {
	return min(max(f, 0), 1)
}

func pct(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

func failStyle(failed int64) lipgloss.Style {
	if failed > 0 {
		return styleFail
	}
	return styleFaint
}

// oneLine 将多行文本压成一行并截断到 limit 个字符。
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// truncLine 截断到指定显示宽度（按 rune 近似）。
func truncLine(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:max(width-1, 1)]) + "…"
}
