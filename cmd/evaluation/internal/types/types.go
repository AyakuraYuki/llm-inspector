// Package types 定义评测框架的核心数据模型：检查项结果、层结果与总报告。
package types

// Status 表示单个检查项的结论状态。
type Status string

const (
	StatusPass        Status = "pass"        // 通过
	StatusFail        Status = "fail"        // 未通过
	StatusUnsupported Status = "unsupported" // 目标服务不支持该能力（不计入层均分）
	StatusSkip        Status = "skip"        // 因配置或前置条件跳过（不计入层均分）
)

// CheckResult 是单个检查项的执行结果。
type CheckResult struct {
	Metrics    map[string]any `json:"metrics,omitempty"`
	Name       string         `json:"name"`
	Status     Status         `json:"status"`
	Detail     string         `json:"detail,omitempty"`
	Score      float64        `json:"score"` // 0..1
	Weight     float64        `json:"weight"`
	DurationMS float64        `json:"duration_ms"`
}

// LayerResult 是一层评测的汇总结果。
type LayerResult struct {
	ID         string        `json:"id"`   // 如 "L1"
	Name       string        `json:"name"` // 如 "API 可用性"
	Reason     string        `json:"reason,omitempty"`
	Checks     []CheckResult `json:"checks"`
	Score      float64       `json:"score"` // 加权平均，仅统计 pass/fail 项
	DurationMS float64       `json:"duration_ms"`
	Enabled    bool          `json:"enabled"`
	Skipped    bool          `json:"skipped"`
	Passed     bool          `json:"passed"`
}

// Compute 根据各检查项计算层得分；threshold 为通过线。
// 仅 pass/fail 状态的检查项参与加权平均。
func (l *LayerResult) Compute(threshold float64) {
	var sum, wSum float64
	for _, c := range l.Checks {
		if c.Status != StatusPass && c.Status != StatusFail {
			continue
		}
		w := c.Weight
		if w <= 0 {
			w = 1
		}
		sum += c.Score * w
		wSum += w
	}
	if wSum > 0 {
		l.Score = sum / wSum
	} else {
		l.Score = 1
	}
	l.Passed = l.Score >= threshold
}

// HasFail 返回该层是否存在 fail 状态的检查项（用于 L1 门控）。
func (l *LayerResult) HasFail() bool {
	for _, c := range l.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// TargetInfo 记录被测目标信息（写入报告）。
type TargetInfo struct {
	BaseURL  string `json:"base_url"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

// Report 是一次评测运行的完整报告。
type Report struct {
	Target     TargetInfo      `json:"target"`
	Tool       string          `json:"tool"`
	Version    string          `json:"version"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at"`
	Verdict    string          `json:"verdict"`
	Layers     []LayerResult   `json:"layers"`
	TotalScore float64         `json:"total_score"`
	Sections   []SectionResult `json:"sections"`          // 三条体检结论
	Summary    *JudgeSummary   `json:"summary,omitempty"` // 裁判总结，judge 未配置时为 nil
}

// LayerWeight 各层在加权参考分（TotalScore）中的权重。
// 注意：该参考分不参与判定，判定以 Sections 与 Verdict 为准。
var LayerWeight = map[string]float64{
	"L1": 0.10,
	"L2": 0.15,
	"L3": 0.30,
	"L4": 0.15,
	"L5": 0.15,
	"L6": 0.15,
}

// ReportSection 标识三条体检结论之一。
type ReportSection string

const (
	// SectionAccess 接入与合规：L1/L2/L6。
	SectionAccess ReportSection = "access"
	// SectionPerf 性能画像：L5。
	SectionPerf ReportSection = "performance"
	// SectionSmoke 可用性冒烟：L3/L4。
	SectionSmoke ReportSection = "smoke"
)

// SectionResult 是单个决策问题的结论。
// Status 取值：pass（通过）/ warn（有短板，不阻断接入）/ fail（不满足接入条件）/ na（未评估）。
type SectionResult struct {
	Section   ReportSection `json:"section"`
	Title     string        `json:"title"`
	Layers    []string      `json:"layers"`
	Status    string        `json:"status"`
	Score     float64       `json:"score"`
	Threshold float64       `json:"threshold"`
	Reasons   []string      `json:"reasons,omitempty"`
}

// JudgeSummary 是裁判模型生成的报告总结。
// Status 取值：ok / error；judge 未配置时 Report.Summary 为 nil。
type JudgeSummary struct {
	Status string `json:"status"`
	Text   string `json:"text,omitempty"`
	Error  string `json:"error,omitempty"`
	Model  string `json:"model,omitempty"`
}
