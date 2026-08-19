package types

import "fmt"

// SectionLayerWeight 各层在 Section 结论内的权重（均权）。
var SectionLayerWeight = map[string]float64{
	"L1": 1, "L2": 1, "L3": 1, "L4": 1, "L5": 1, "L6": 1,
}

// ComputeSections 由层结果计算三条体检结论。
// threshold 沿用 cfg.Thresholds.MinLayerScore。
// 判定口径：
//   - 接入与合规（L1/L2/L6）：全部 Passed → pass；任一 fail → fail（无 warn 档，
//     接入问题没有灰色地带）；全部未启用/跳过 → na。
//   - 性能画像（L5）：Passed → pass；未达标 → warn（信息性结论，SLO 未达只提醒
//     接入方选型）；未启用/跳过 → na。
//   - 可用性冒烟（L3/L4）：全部 Passed → pass；任一 fail → warn（冒烟失败只表示
//     能力/稳定性不足，不是接入阻断项）；全部 na → na。
func ComputeSections(layers []LayerResult, threshold float64) []SectionResult {
	sectionDef := map[ReportSection]struct {
		title  string
		layers []string
		warnOK bool // 是否允许 warn 档
	}{
		SectionAccess: {"接入与合规", []string{"L1", "L2", "L6"}, false},
		SectionPerf:   {"性能画像", []string{"L5"}, true},
		SectionSmoke:  {"可用性冒烟", []string{"L3", "L4"}, true},
	}

	sections := make([]SectionResult, 0, 3)
	for _, sec := range []ReportSection{SectionAccess, SectionPerf, SectionSmoke} {
		def := sectionDef[sec]
		sr := SectionResult{
			Section:   sec,
			Title:     def.title,
			Layers:    def.layers,
			Threshold: threshold,
		}

		// 收集参与层（已启用且未跳过的指定层）
		var participating []LayerResult
		for _, l := range layers {
			for _, lid := range def.layers {
				if l.ID == lid && l.Enabled && !l.Skipped {
					participating = append(participating, l)
					break
				}
			}
		}

		if len(participating) == 0 {
			sr.Status = "na"
			sr.Score = 0
		} else {
			allPass := true
			anyFail := false
			var sum, wSum float64
			for _, l := range participating {
				w := SectionLayerWeight[l.ID]
				if w <= 0 {
					w = 1
				}
				sum += l.Score * w
				wSum += w
				if !l.Passed {
					allPass = false
					anyFail = true // Passed=false 即 Score < threshold（Compute 后二者等价）
				}
			}
			sr.Score = sum / wSum

			switch {
			case allPass:
				sr.Status = "pass"
			case anyFail && def.warnOK:
				sr.Status = "warn"
			default:
				sr.Status = "fail"
			}
		}

		// 提取 Reasons：每个未达标层一条（层名 + 得分 + 至多 2 条 fail 检查项），
		// 供报告渲染顶部直接引用。
		for _, l := range participating {
			if !l.Passed {
				reason := fmt.Sprintf("%s %s 得分 %.0f%%", l.ID, l.Name, l.Score*100)
				n := 0
				for _, c := range l.Checks {
					if c.Status != StatusFail || n >= 2 {
						continue
					}
					reason += "; " + c.Name + ": " + truncateRunes(c.Detail, 60)
					n++
				}
				sr.Reasons = append(sr.Reasons, reason)
			}
		}

		sections = append(sections, sr)
	}
	return sections
}

// truncateRunes 按 rune 截断字符串，超过 n 时以 "…" 结尾。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
