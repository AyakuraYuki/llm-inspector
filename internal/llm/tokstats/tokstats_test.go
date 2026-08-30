package tokstats

import (
	"testing"
	"time"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int64
	}{
		{"空字符串", "", 0},
		{"纯 ASCII 按 4 字符/token", "abcdefghijklmnopqrst", 5}, // 20 chars
		{"纯中文按 1.5 字符/token", "机器学习是人工智能的一个分支领域", 10},       // 15 字
		// "hello " = 6 ASCII → 6/4 = 1；"机器学习世界" = 6 字 → 6/1.5 = 4
		{"混合文本按构成加权", "hello 机器学习世界", 5},
		{"单字符兜底为 1", "a", 1},
		{"单中文字符兜底为 1", "好", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.text); got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

func TestValidGenWindow(t *testing.T) {
	tests := []struct {
		name      string
		genWindow time.Duration
		e2e       time.Duration
		want      bool
	}{
		{"正常窗口", 10 * time.Second, 11 * time.Second, true},
		{"恰好达到绝对下限且比例满足", 50 * time.Millisecond, time.Second, true}, // 50ms = 5% of 1s
		{"低于绝对下限", 49 * time.Millisecond, time.Second, false},
		{"绝对值够但占比不足", 300 * time.Millisecond, 30 * time.Second, false}, // 1%
		{"零窗口", 0, 10 * time.Second, false},
		{"负窗口（TTFT 晚于 E2E 的异常样本）", -time.Second, 10 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidGenWindow(tt.genWindow, tt.e2e); got != tt.want {
				t.Errorf("ValidGenWindow(%v, %v) = %v, want %v", tt.genWindow, tt.e2e, got, tt.want)
			}
		})
	}
}

func TestValidStreamTPS(t *testing.T) {
	defer func() { MaxPlausibleStreamTPS = 4096 }() // 防止用例间串扰

	tests := []struct {
		name      string
		tokens    int64
		genWindow time.Duration
		e2e       time.Duration
		want      bool
	}{
		{"正常解码速度", 1000, 10 * time.Second, 11 * time.Second, true},    // 100 tok/s
		{"贴着天花板", 40960, 10 * time.Second, 11 * time.Second, true},    // 4096 tok/s
		{"超过物理天花板", 40961, 10 * time.Second, 11 * time.Second, false}, // > 4096 tok/s
		{"窗口过窄（双门槛剔除）", 500, 50 * time.Microsecond, 10 * time.Second, false},
		{"占比不足（双门槛剔除）", 3000, 300 * time.Millisecond, 30 * time.Second, false},
		{"无 token", 0, 10 * time.Second, 11 * time.Second, false},
		{"负 token", -5, 10 * time.Second, 11 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidStreamTPS(tt.tokens, tt.genWindow, tt.e2e); got != tt.want {
				t.Errorf("ValidStreamTPS(%d, %v, %v) = %v, want %v",
					tt.tokens, tt.genWindow, tt.e2e, got, tt.want)
			}
		})
	}
}
