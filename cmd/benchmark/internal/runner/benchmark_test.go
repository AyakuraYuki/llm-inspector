package runner

import "testing"

func Test_extractAnswer(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "简单情况：正文里的 boxed",
			response: `The answer is \boxed{504}.`,
			want:     "504",
		},
		{
			name:     "没有 boxed",
			response: `The answer is 504.`,
			want:     "",
		},
		{
			name: "思考内容里出现空 boxed，正文给出正确答案",
			response: `<think>
Need final boxed. Use \boxed{}.
We should ensure final answer not overly terse. Use \boxed{240}.
</think>
Their sum is
8+32+200=\boxed{240}.`,
			want: "240",
		},
		{
			name: "思考里出现干扰性的错误 boxed，正文才是最终答案",
			response: `<think>
maybe \boxed{99} but let me recheck.
</think>
final \boxed{240}.`,
			want: "240",
		},
		{
			name:     "带嵌套大括号（分数）",
			response: `<think>reasoning</think> so \boxed{\frac{1}{2}}`,
			want:     `\frac{1}{2}`,
		},
		{
			name:     "只有闭合标签、缺失起始标签",
			response: `some reasoning \boxed{7} more</think> final \boxed{42}`,
			want:     "42",
		},
		{
			name:     "thinking 标签变体",
			response: `<thinking>\boxed{1}</thinking>\boxed{2}`,
			want:     "2",
		},
		{
			name:     "回退：正文没有 boxed，只有思考内容里有",
			response: `<think>after much work, \boxed{88}</think> so the answer follows.`,
			want:     "88",
		},
		{
			name:     "多个正文 boxed，取最后一个",
			response: `first \boxed{1}, then corrected to \boxed{2}`,
			want:     "2",
		},
		{
			name:     "正文最后一个 boxed 为空，回退到前一个非空 boxed",
			response: `\boxed{15} is the answer. \boxed{}`,
			want:     "15",
		},
		{
			name:     "未闭合的 boxed（被截断），回退到前一个完整 boxed",
			response: `\boxed{30} then \boxed{unclosed`,
			want:     "30",
		},
		{
			name:     "答案含换行需清理",
			response: "\\boxed{2\n40}",
			want:     "2 40",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAnswer(tt.response); got != tt.want {
				t.Errorf("extractAnswer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func Test_stripReasoning(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "无思考标签，原样返回",
			response: `plain answer`,
			want:     `plain answer`,
		},
		{
			name:     "标准 think 标签",
			response: "<think>reasoning here</think>answer",
			want:     "answer",
		},
		{
			name:     "多种标签共存，取最靠后的切割点",
			response: "</thinking>a</think>b",
			want:     "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripReasoning(tt.response); got != tt.want {
				t.Errorf("stripReasoning() = %q, want %q", got, tt.want)
			}
		})
	}
}
