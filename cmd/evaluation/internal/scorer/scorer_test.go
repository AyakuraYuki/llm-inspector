package scorer

import (
	"testing"
)

func TestScoreDeterministic(t *testing.T) {
	tests := []struct {
		name      string
		spec      Spec
		output    string
		wantScore float64
		wantErr   bool
	}{
		{"exact_match/通过", Spec{Type: "exact_match", Expected: "olleh"}, "olleh", 1, false},
		{"exact_match/归一化标点", Spec{Type: "exact_match", Expected: "巴黎"}, "巴黎。", 1, false},
		{"exact_match/不匹配", Spec{Type: "exact_match", Expected: "olleh"}, "hello", 0, false},
		{"contains/全含", Spec{Type: "contains", Keywords: []string{"神经网络", "梯度"}}, "深度学习基于神经网络，使用梯度下降", 1, false},
		{"contains/缺一个", Spec{Type: "contains", Keywords: []string{"a", "b"}}, "只有 a", 0.5, false},
		{"contains/下标数字归一", Spec{Type: "contains", Keywords: []string{"H2O"}}, "水的化学式是 H₂O", 1, false},
		{"contains/全角数字归一", Spec{Type: "contains", Keywords: []string{"395"}}, "答案是 ３９５", 1, false},
		{"exact_match/上标归一", Spec{Type: "exact_match", Expected: "x2"}, "x²", 1, false},
		{"numeric/全角数字", Spec{Type: "numeric", Expected: 395}, "３９５", 1, false},
		{"regex/匹配", Spec{Type: "regex", Pattern: `^\d{4}$`}, "1024", 1, false},
		{"regex/不匹配", Spec{Type: "regex", Pattern: `^\d{4}$`}, "abc", 0, false},
		{"regex/无效表达式", Spec{Type: "regex", Pattern: `[`}, "x", 0, true},
		{"numeric/精确", Spec{Type: "numeric", Expected: 395}, "395", 1, false},
		{"numeric/提取尾部数字", Spec{Type: "numeric", Expected: 395}, "答案是 395", 1, false},
		{"numeric/错误值", Spec{Type: "numeric", Expected: 395}, "396", 0, false},
		{"numeric/无数字", Spec{Type: "numeric", Expected: 1}, "没有数字", 0, false},
		{"json_valid/合法", Spec{Type: "json_valid"}, `{"a":1}`, 1, false},
		{"json_valid/带围栏", Spec{Type: "json_valid"}, "```json\n{\"a\":1}\n```", 1, false},
		{"json_valid/非法", Spec{Type: "json_valid"}, "不是 JSON", 0, false},
		{"json_schema/字段齐全", Spec{Type: "json_schema", Fields: []string{"name", "age"}}, `{"name":"x","age":3}`, 1, false},
		{"json_schema/缺字段", Spec{Type: "json_schema", Fields: []string{"name", "age"}}, `{"name":"x"}`, 0.5, false},
		{"bullet_count/符合", Spec{Type: "bullet_count", Expected: 3}, "- 一\n- 二\n- 三", 1, false},
		{"bullet_count/数字列表", Spec{Type: "bullet_count", Expected: 2}, "1. 一\n2. 二", 1, false},
		{"bullet_count/不符", Spec{Type: "bullet_count", Expected: 3}, "没有要点", 0, false},
		{"keyword/必含且禁含", Spec{Type: "keyword", Keywords: []string{"好"}, Forbidden: []string{"坏"}}, "很好", 1, false},
		{"keyword/触发禁含", Spec{Type: "keyword", Keywords: []string{"好"}, Forbidden: []string{"坏"}}, "好坏", 0.5, false},
		{"lowercase/通过", Spec{Type: "lowercase"}, "tokyo", 1, false},
		{"lowercase/失败", Spec{Type: "lowercase"}, "Tokyo", 0, false},
		{"judge/应报错", Spec{Type: "judge"}, "任意", 0, true},
		{"未知类型", Spec{Type: "nope"}, "任意", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Score(&tt.spec, tt.output)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if v.Score != tt.wantScore {
				t.Fatalf("score = %v, want %v（reason: %s）", v.Score, tt.wantScore, v.Reason)
			}
		})
	}
}
