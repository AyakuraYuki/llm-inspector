package util

func Ternary[T any](condition bool, ifOutput T, elseOutput T) T {
	if condition {
		return ifOutput
	}
	return elseOutput
}

func TernaryF[T any](condition bool, ifFunc func() T, elseFunc func() T) T {
	if condition {
		return ifFunc()
	}
	return elseFunc()
}

func Enabled(b *bool) bool {
	return b == nil || *b
}

func Selected(m map[string]bool, key string, enabled *bool) bool {
	if len(m) > 0 && !m[key] {
		return false
	}
	return Enabled(enabled)
}
