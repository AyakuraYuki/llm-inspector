package stats

import "testing"

func TestPercentile(t *testing.T) {
	samples := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := Percentile(samples, 50); got != 5.5 {
		t.Fatalf("p50 = %v, want 5.5", got)
	}
	if got := Percentile(samples, 0); got != 1 {
		t.Fatalf("p0 = %v, want 1", got)
	}
	if got := Percentile(samples, 100); got != 10 {
		t.Fatalf("p100 = %v, want 10", got)
	}
	if got := Percentile(nil, 99); got != 0 {
		t.Fatalf("空切片 = %v, want 0", got)
	}
	// 乱序输入也应正确
	if got := Percentile([]float64{10, 1, 5}, 50); got != 5 {
		t.Fatalf("乱序 p50 = %v, want 5", got)
	}
}

func TestMean(t *testing.T) {
	if got := Mean([]float64{1, 2, 3}); got != 2 {
		t.Fatalf("Mean = %v, want 2", got)
	}
	if got := Mean(nil); got != 0 {
		t.Fatalf("空切片 Mean = %v, want 0", got)
	}
}
