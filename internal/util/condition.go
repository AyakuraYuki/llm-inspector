// Package util 提供各子命令共用的小工具函数。
package util

// Enabled 判断可选布尔开关是否启用，未显式配置（nil）时视为启用。
func Enabled(b *bool) bool {
	return b == nil || *b
}
