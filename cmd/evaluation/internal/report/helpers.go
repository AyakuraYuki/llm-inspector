package report

import (
	"fmt"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// KeySignal 是报告顶部提炼的一条关键信号。
type KeySignal struct {
	Label string
	Value string
}

// extractKeySignals 从各层提取值得关注的关键信号。
// 已知 metrics 键按层硬编码，类型断言失败时跳过（不 panic）。
func extractKeySignals(r *types.Report) []KeySignal {
	var signals []KeySignal

	for _, l := range r.Layers {
		switch l.ID {
		case "L2":
			// 伪流式提示：直接复用 checkStreaming 的 Detail 字段（文案已拼好）
			if c := findCheck(&l, "streaming_sse"); c != nil && c.Status != types.StatusSkip {
				if ttft := metricFloat(c.Metrics, "ttft_ms"); ttft >= 0 {
					if ratio := metricFloat(c.Metrics, "ttft_ratio"); ratio > 0.9 {
						signals = append(signals, KeySignal{"疑似伪流式转发", "首内容占总耗时 " + fmt.Sprintf("%.0f%%", ratio*100)})
					}
				}
			}
		case "L5":
			if c := findCheck(&l, "latency_ttft"); c != nil {
				p99 := metricFloat(c.Metrics, "ttft_p99_ms")
				slo := metricFloat(c.Metrics, "slo_ttft_p99_ms")
				if p99 >= 0 {
					val := fmt.Sprintf("%.0fms", p99)
					if slo > 0 {
						val += fmt.Sprintf("（SLO %.0fms）", slo)
					}
					signals = append(signals, KeySignal{"TTFT P99", val})
				}
			}
			if c := findCheck(&l, "throughput"); c != nil {
				if mean := metricFloat(c.Metrics, "tps_mean"); mean >= 0 {
					val := fmt.Sprintf("%.1f tok/s", mean)
					if slo := metricFloat(c.Metrics, "slo_min_tps"); slo > 0 {
						val += fmt.Sprintf("（SLO %.1f）", slo)
					}
					signals = append(signals, KeySignal{"单流吞吐均值", val})
				}
			}
		}
	}

	// 关键失败项（跨层，最多列 5 条）
	failCount := 0
	for _, l := range r.Layers {
		for _, c := range l.Checks {
			if c.Status == types.StatusFail && failCount < 5 {
				signals = append(signals, KeySignal{
					"失败: " + l.ID + "/" + c.Name,
					oneLine(c.Detail, 80),
				})
				failCount++
			}
		}
	}
	return signals
}

func findCheck(l *types.LayerResult, name string) *types.CheckResult {
	for i := range l.Checks {
		if l.Checks[i].Name == name {
			return &l.Checks[i]
		}
	}
	return nil
}

// metricFloat 从 metrics 中取 float64 值；缺失或类型不符返回 -1。
func metricFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return -1
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return -1
	}
}
