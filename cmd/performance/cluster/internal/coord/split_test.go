package coord

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		name   string
		global int
		n      int
		want   []int
	}{
		{"整除", 100, 4, []int{25, 25, 25, 25}},
		{"余数摊给前几台", 10, 3, []int{4, 3, 3}},
		{"节点多于并发", 2, 4, []int{1, 1, 0, 0}},
		{"单节点", 5000, 1, []int{5000}},
		{"目标并发 5000 四节点", 5000, 4, []int{1250, 1250, 1250, 1250}},
		{"非法并发", 0, 3, []int{0, 0, 0}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Split(c.global, c.n)
			if len(got) != len(c.want) {
				t.Fatalf("Split(%d, %d) 长度 = %d, want %d", c.global, c.n, len(got), len(c.want))
			}
			sum := 0
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("Split(%d, %d)[%d] = %d, want %d", c.global, c.n, i, got[i], c.want[i])
				}
				sum += got[i]
			}
			if c.global > 0 && sum != c.global {
				t.Errorf("Split(%d, %d) 份额总和 = %d, want %d", c.global, c.n, sum, c.global)
			}
		})
	}
}

// TestSplitMonotonicPerIndex 验证同一下标的份额随全局并发单调不减：
// session 建立时按 Split(max)[i] 一次性配置连接池的前提就是这条性质。
func TestSplitMonotonicPerIndex(t *testing.T) {
	const n = 7
	prev := Split(0, n)
	for global := 1; global <= 500; global++ {
		cur := Split(global, n)
		for i := range cur {
			if cur[i] < prev[i] {
				t.Fatalf("Split(%d, %d)[%d] = %d < Split(%d, %d)[%d] = %d，份额非单调",
					global, n, i, cur[i], global-1, n, i, prev[i])
			}
		}
		prev = cur
	}
}
