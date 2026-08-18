// Package util 提供各子命令共用的小工具函数。
package util

// Ternary 返回三元表达式的结果：condition 为真时返回 ifOutput，否则返回 elseOutput。
func Ternary[T any](condition bool, ifOutput T, elseOutput T) T {
	if condition {
		return ifOutput
	}
	return elseOutput
}

// TernaryF 是 Ternary 的惰性版本，只对被选中的分支求值。
func TernaryF[T any](condition bool, ifFunc func() T, elseFunc func() T) T {
	if condition {
		return ifFunc()
	}
	return elseFunc()
}

// Enabled 判断可选布尔开关是否启用，未显式配置（nil）时视为启用。
func Enabled(b *bool) bool {
	return b == nil || *b
}
