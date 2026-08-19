package runner

import (
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

func TestDeriveVerdict(t *testing.T) {
	sections := func(access, smoke string) []types.SectionResult {
		return []types.SectionResult{
			{Section: types.SectionAccess, Status: access},
			{Section: types.SectionPerf, Status: "pass"},
			{Section: types.SectionSmoke, Status: smoke},
		}
	}

	tests := []struct {
		name     string
		executed int
		sections []types.SectionResult
		want     string
	}{
		{"接入通过且冒烟通过", 6, sections("pass", "pass"), "pass"},
		{"接入通过但冒烟有短板", 6, sections("pass", "warn"), "pass_with_warnings"},
		{"接入通过且冒烟未评估", 6, sections("pass", "na"), "pass"},
		{"接入未通过", 6, sections("fail", "pass"), "fail"},
		{"接入未评估（诊断运行）", 1, sections("na", "na"), "pass"},
		{"未执行任何层", 0, nil, "no_layers_executed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveVerdict(tt.executed, tt.sections); got != tt.want {
				t.Errorf("deriveVerdict = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComputeSections(t *testing.T) {
	layer := func(id string, passed bool, score float64) types.LayerResult {
		l := types.LayerResult{ID: id, Name: id, Enabled: true, Passed: passed, Score: score}
		if !passed {
			l.Checks = append(l.Checks, types.CheckResult{Name: "c1", Status: types.StatusFail, Detail: "失败详情"})
		}
		return l
	}

	t.Run("全过", func(t *testing.T) {
		layers := []types.LayerResult{
			layer("L1", true, 1), layer("L2", true, 1), layer("L3", true, 1),
			layer("L4", true, 1), layer("L5", true, 1), layer("L6", true, 1),
		}
		s := types.ComputeSections(layers, 0.8)
		if got := statusOf(s, types.SectionAccess); got != "pass" {
			t.Errorf("access = %q, want pass", got)
		}
		if got := statusOf(s, types.SectionPerf); got != "pass" {
			t.Errorf("perf = %q, want pass", got)
		}
		if got := statusOf(s, types.SectionSmoke); got != "pass" {
			t.Errorf("smoke = %q, want pass", got)
		}
	})

	t.Run("L2失败则接入fail", func(t *testing.T) {
		layers := []types.LayerResult{
			layer("L1", true, 1), layer("L2", false, 0.5), layer("L3", true, 1),
			layer("L4", true, 1), layer("L5", true, 1), layer("L6", true, 1),
		}
		s := types.ComputeSections(layers, 0.8)
		if got := statusOf(s, types.SectionAccess); got != "fail" {
			t.Errorf("access = %q, want fail", got)
		}
		if got := statusOf(s, types.SectionSmoke); got != "pass" {
			t.Errorf("smoke = %q, want pass", got)
		}
	})

	t.Run("L5未达标则性能warn", func(t *testing.T) {
		layers := []types.LayerResult{
			layer("L1", true, 1), layer("L2", true, 1), layer("L3", true, 1),
			layer("L4", true, 1), layer("L5", false, 0.6), layer("L6", true, 1),
		}
		s := types.ComputeSections(layers, 0.8)
		if got := statusOf(s, types.SectionAccess); got != "pass" {
			t.Errorf("access = %q, want pass", got)
		}
		if got := statusOf(s, types.SectionPerf); got != "warn" {
			t.Errorf("perf = %q, want warn", got)
		}
	})

	t.Run("L3失败则冒烟warn", func(t *testing.T) {
		layers := []types.LayerResult{
			layer("L1", true, 1), layer("L2", true, 1), layer("L3", false, 0.7),
			layer("L4", true, 1), layer("L5", true, 1), layer("L6", true, 1),
		}
		s := types.ComputeSections(layers, 0.8)
		if got := statusOf(s, types.SectionSmoke); got != "warn" {
			t.Errorf("smoke = %q, want warn", got)
		}
	})

	t.Run("全部未启用则na", func(t *testing.T) {
		var layers []types.LayerResult
		s := types.ComputeSections(layers, 0.8)
		for _, sec := range []types.ReportSection{types.SectionAccess, types.SectionPerf, types.SectionSmoke} {
			if got := statusOf(s, sec); got != "na" {
				t.Errorf("%s = %q, want na", sec, got)
			}
		}
	})
}

func statusOf(sections []types.SectionResult, sec types.ReportSection) string {
	for _, s := range sections {
		if s.Section == sec {
			return s.Status
		}
	}
	return "na"
}
