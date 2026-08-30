package util

import "strings"

// TruncateString 按 rune 截断字符串到 n 个字符，超长时追加省略号。
func TruncateString(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// StripCodeFence 去掉 ```json ... ``` 之类的 Markdown 代码围栏。
func StripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

// MaxLen 返回字符串切片中最长者的字符数，用于对齐动态标签（如 finish_reason、
// 数据集名）。空切片返回 0。
func MaxLen(strs []string) int {
	m := 0
	for _, s := range strs {
		if n := len([]rune(s)); n > m {
			m = n
		}
	}
	return m
}
