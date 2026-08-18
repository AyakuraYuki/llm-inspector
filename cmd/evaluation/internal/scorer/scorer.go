// Package scorer 提供确定性打分器与裁判模型打分器。
package scorer

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// Spec 描述一个打分器的配置（数据集 YAML 中的 scorer 字段）。
type Spec struct {
	Expected  any      `yaml:"expected" json:"expected,omitempty"`   // exact_match/bullet_count/numeric 的期望值
	Type      string   `yaml:"type" json:"type"`                     // exact_match/contains/regex/numeric/json_valid/json_schema/bullet_count/keyword/lowercase/judge
	Pattern   string   `yaml:"pattern" json:"pattern,omitempty"`     // regex 模式
	Rubric    string   `yaml:"rubric" json:"rubric,omitempty"`       // judge 评分标准
	Keywords  []string `yaml:"keywords" json:"keywords,omitempty"`   // contains/keyword 必含词
	Forbidden []string `yaml:"forbidden" json:"forbidden,omitempty"` // keyword 禁含词
	Fields    []string `yaml:"fields" json:"fields,omitempty"`       // json_schema 必需字段
	Tolerance float64  `yaml:"tolerance" json:"tolerance,omitempty"` // numeric 容差（相对）
}

// Verdict 是一次打分的结果。
type Verdict struct {
	Reason string
	Score  float64 // 0..1
}

// Score 使用确定性打分器评估模型输出。judge 类型由 Judge.Score 处理。
func Score(spec *Spec, output string) (Verdict, error) {
	out := strings.TrimSpace(output)
	switch spec.Type {
	case "exact_match":
		return scoreExactMatch(spec, out), nil
	case "contains":
		return scoreContains(spec, out), nil
	case "regex":
		return scoreRegex(spec, out)
	case "numeric":
		return scoreNumeric(spec, out), nil
	case "json_valid":
		return scoreJSONValid(out), nil
	case "json_schema":
		return scoreJSONSchema(spec, out), nil
	case "bullet_count":
		return scoreBulletCount(spec, out), nil
	case "keyword":
		return scoreKeyword(spec, out), nil
	case "lowercase":
		return scoreLowercase(out), nil
	case "judge":
		return Verdict{}, fmt.Errorf("judge 打分器需要裁判模型，请使用 Judge.Score")
	default:
		return Verdict{}, fmt.Errorf("未知打分器类型: %q", spec.Type)
	}
}

// digitFold 将上下标数字与全角数字归一为 ASCII 数字，
// 使化学式（H₂O）、上下标（x²）、全角数字（３９５）等高阶输出形式可按字面量匹配。
var digitFold = strings.NewReplacer(
	"₀", "0", "₁", "1", "₂", "2", "₃", "3", "₄", "4", "₅", "5", "₆", "6", "₇", "7", "₈", "8", "₉", "9",
	"⁰", "0", "¹", "1", "²", "2", "³", "3", "⁴", "4", "⁵", "5", "⁶", "6", "⁷", "7", "⁸", "8", "⁹", "9",
	"０", "0", "１", "1", "２", "2", "３", "3", "４", "4", "５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
)

// fold 归一化大小写与数字形式，用于 contains/keyword 等子串匹配。
func fold(s string) string {
	return strings.ToLower(digitFold.Replace(s))
}

// normalize 做宽松归一化：去空白、统一大小写、去掉收尾标点与代码块围栏、归一数字形式。
func normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`")
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "。．.!！?？;；")
	return fold(s)
}

func scoreExactMatch(spec *Spec, out string) Verdict {
	want := normalize(fmt.Sprint(spec.Expected))
	got := normalize(out)
	// 输出可能带解释，允许"归一化后相等"或"输出仅由期望答案构成"
	if got == want {
		return Verdict{Score: 1, Reason: "完全匹配"}
	}
	return Verdict{Score: 0, Reason: fmt.Sprintf("期望 %q，实际 %q", fmt.Sprint(spec.Expected), util.TruncateString(out, 80))}
}

func scoreContains(spec *Spec, out string) Verdict {
	lower := fold(out)
	var missing []string
	for _, kw := range spec.Keywords {
		if !strings.Contains(lower, fold(kw)) {
			missing = append(missing, kw)
		}
	}
	if len(missing) == 0 {
		return Verdict{Score: 1, Reason: "包含全部关键词"}
	}
	score := float64(len(spec.Keywords)-len(missing)) / float64(len(spec.Keywords))
	return Verdict{Score: score, Reason: fmt.Sprintf("缺少关键词: %s", strings.Join(missing, ", "))}
}

func scoreRegex(spec *Spec, out string) (Verdict, error) {
	re, err := regexp.Compile(spec.Pattern)
	if err != nil {
		return Verdict{}, fmt.Errorf("无效正则 %q: %w", spec.Pattern, err)
	}
	if re.MatchString(out) {
		return Verdict{Score: 1, Reason: "正则匹配"}, nil
	}
	return Verdict{Score: 0, Reason: fmt.Sprintf("正则不匹配: %s", spec.Pattern)}, nil
}

var numberRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// extractNumber 从输出中提取最后一个数字（模型常在结尾给答案）。
// 提取前将上下标/全角数字归一为 ASCII。
func extractNumber(s string) (float64, bool) {
	matches := numberRe.FindAllString(digitFold.Replace(s), -1)
	if len(matches) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(matches[len(matches)-1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func scoreNumeric(spec *Spec, out string) Verdict {
	want, err := strconv.ParseFloat(fmt.Sprint(spec.Expected), 64)
	if err != nil {
		// 期望值可能是带逗号的数字
		want, err = strconv.ParseFloat(strings.ReplaceAll(fmt.Sprint(spec.Expected), ",", ""), 64)
		if err != nil {
			return Verdict{Score: 0, Reason: fmt.Sprintf("期望值 %v 不是数字", spec.Expected)}
		}
	}
	got, ok := extractNumber(out)
	if !ok {
		return Verdict{Score: 0, Reason: "输出中没有数字"}
	}
	tol := spec.Tolerance
	if tol <= 0 {
		tol = 1e-9
	}
	if math.Abs(got-want) <= math.Abs(want)*tol+1e-9 {
		return Verdict{Score: 1, Reason: fmt.Sprintf("数值匹配: %v", got)}
	}
	return Verdict{Score: 0, Reason: fmt.Sprintf("期望 %v，实际提取 %v", want, got)}
}

func scoreJSONValid(out string) Verdict {
	s := stripCodeFence(out)
	if json.Valid([]byte(s)) {
		return Verdict{Score: 1, Reason: "合法 JSON"}
	}
	return Verdict{Score: 0, Reason: "输出不是合法 JSON"}
}

func scoreJSONSchema(spec *Spec, out string) Verdict {
	s := stripCodeFence(out)
	var obj any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return Verdict{Score: 0, Reason: "输出不是合法 JSON"}
	}
	m, ok := obj.(map[string]any)
	if !ok {
		return Verdict{Score: 0, Reason: "JSON 顶层不是对象"}
	}
	var missing []string
	for _, f := range spec.Fields {
		if _, exists := m[f]; !exists {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return Verdict{Score: 1, Reason: "字段齐全"}
	}
	score := float64(len(spec.Fields)-len(missing)) / float64(len(spec.Fields))
	return Verdict{Score: score, Reason: fmt.Sprintf("缺少字段: %s", strings.Join(missing, ", "))}
}

var bulletRe = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.、)])\s`)

func scoreBulletCount(spec *Spec, out string) Verdict {
	want, err := strconv.Atoi(fmt.Sprint(spec.Expected))
	if err != nil {
		return Verdict{Score: 0, Reason: fmt.Sprintf("期望值 %v 不是整数", spec.Expected)}
	}
	got := len(bulletRe.FindAllString(out, -1))
	if got == want {
		return Verdict{Score: 1, Reason: fmt.Sprintf("要点数 %d", got)}
	}
	return Verdict{Score: 0, Reason: fmt.Sprintf("期望 %d 个要点，实际 %d 个", want, got)}
}

func scoreKeyword(spec *Spec, out string) Verdict {
	lower := fold(out)
	total := len(spec.Keywords) + len(spec.Forbidden)
	if total == 0 {
		return Verdict{Score: 0, Reason: "keyword 打分器需要 keywords 或 forbidden"}
	}
	var bad []string
	for _, kw := range spec.Keywords {
		if !strings.Contains(lower, fold(kw)) {
			bad = append(bad, "缺少:"+kw)
		}
	}
	for _, kw := range spec.Forbidden {
		if strings.Contains(lower, fold(kw)) {
			bad = append(bad, "禁含:"+kw)
		}
	}
	if len(bad) == 0 {
		return Verdict{Score: 1, Reason: "关键词约束全部满足"}
	}
	return Verdict{Score: float64(total-len(bad)) / float64(total), Reason: strings.Join(bad, ", ")}
}

func scoreLowercase(out string) Verdict {
	if out == strings.ToLower(out) {
		return Verdict{Score: 1, Reason: "全部小写"}
	}
	return Verdict{Score: 0, Reason: "存在大写字符"}
}

// stripCodeFence 去掉 ```json ... ``` 围栏。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
