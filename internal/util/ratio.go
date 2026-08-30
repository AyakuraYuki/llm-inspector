package util

// CacheHitRatio 计算缓存命中率（百分比）：cachedTokens / totalTokens * 100。
// 调用方需自行保证 totalTokens > 0。
func CacheHitRatio(cachedTokens, totalTokens int64) float64 {
	return float64(cachedTokens) / float64(totalTokens) * 100
}
