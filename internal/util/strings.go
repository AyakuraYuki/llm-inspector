package util

// TruncateString 按 rune 截断字符串到 n 个字符，超长时追加省略号。
func TruncateString(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
