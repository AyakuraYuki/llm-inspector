package runner

import "testing"

func TestComputeVerdict(t *testing.T) {
	tests := []struct {
		name      string
		executed  int
		allPassed bool
		total     float64
		threshold float64
		want      string
	}{
		{"全部层达标", 5, true, 0.95, 0.8, "pass"},
		{"总评达标但个别层未达标", 5, false, 0.949, 0.8, "pass_with_warnings"},
		{"总评不达标", 5, false, 0.75, 0.8, "fail"},
		{"未执行任何层", 0, true, 0, 0.8, "no_layers_executed"},
		{"总评恰好在阈值", 5, false, 0.8, 0.8, "pass_with_warnings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeVerdict(tt.executed, tt.allPassed, tt.total, tt.threshold); got != tt.want {
				t.Errorf("computeVerdict = %q, want %q", got, tt.want)
			}
		})
	}
}
