package runner

import "testing"

func TestShouldStopEarly(t *testing.T) {
	cases := []struct {
		name         string
		total        int64
		failed       int64
		minSamples   int
		maxErrorRate float64
		want         bool
	}{
		{"样本不足不触发", 10, 10, 20, 0.5, false},
		{"样本达标但错误率未超阈值", 20, 5, 20, 0.5, false},
		{"样本达标且错误率超阈值", 20, 11, 20, 0.5, true},
		{"样本达标且错误率恰好等于阈值不触发", 20, 10, 20, 0.5, false},
		{"全部失败必然触发", 20, 20, 20, 0.5, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ShouldStopEarly(c.total, c.failed, c.minSamples, c.maxErrorRate)
			if got != c.want {
				t.Errorf("ShouldStopEarly(%d, %d, %d, %v) = %v, want %v",
					c.total, c.failed, c.minSamples, c.maxErrorRate, got, c.want)
			}
		})
	}
}
