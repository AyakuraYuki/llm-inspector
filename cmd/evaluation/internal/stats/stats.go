// Package stats 提供评测所需的基础统计函数。
package stats

import "sort"

// Percentile 返回样本的第 p 百分位数（p 取 0..100，线性插值）。空切片返回 0。
func Percentile(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	s := make([]float64, len(samples))
	copy(s, samples)
	sort.Float64s(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	idx := p / 100 * float64(len(s)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(s) {
		return s[lo]
	}
	frac := idx - float64(lo)
	return s[lo]*(1-frac) + s[hi]*frac
}

// Mean 返回样本均值。空切片返回 0。
func Mean(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		sum += v
	}
	return sum / float64(len(samples))
}
