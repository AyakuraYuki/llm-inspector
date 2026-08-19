package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// buildFixtureReport 构造一个覆盖三结论、关键信号、总结三态的示例报告。
func buildFixtureReport() *types.Report {
	layer := func(id, name string, passed bool, score float64, checks ...types.CheckResult) types.LayerResult {
		l := types.LayerResult{ID: id, Name: name, Enabled: true, Passed: passed, Score: score, Checks: checks}
		return l
	}
	check := func(name string, status types.Status, score float64, detail string, metrics map[string]any) types.CheckResult {
		return types.CheckResult{Name: name, Status: status, Score: score, Detail: detail, Metrics: metrics}
	}

	return &types.Report{
		Target:    types.TargetInfo{BaseURL: "http://mock/v1", Model: "mock-model", Protocol: "openai"},
		StartedAt: "2026-01-01T00:00:00Z",
		Verdict:   "pass_with_warnings",
		Layers: []types.LayerResult{
			layer("L1", "API 可用性", true, 1,
				check("models_endpoint", types.StatusPass, 1, "", nil)),
			layer("L2", "协议兼容性", false, 0.8,
				check("streaming_sse", types.StatusPass, 1, "", map[string]any{"ttft_ms": float64(100), "ttft_ratio": float64(0.95)}),
				check("json_schema", types.StatusFail, 0, "输出不是合法 JSON", nil)),
			layer("L3", "模型能力", true, 1),
			layer("L4", "稳定性", true, 0.9),
			layer("L5", "模型性能", true, 0.95,
				check("latency_ttft", types.StatusPass, 1, "", map[string]any{"ttft_p99_ms": float64(1850), "slo_ttft_p99_ms": float64(2000)}),
				check("throughput", types.StatusPass, 1, "", map[string]any{"tps_mean": float64(12.5), "slo_min_tps": float64(10)})),
			layer("L6", "参数边界与健壮性", true, 1),
		},
		Sections: types.ComputeSections([]types.LayerResult{
			layer("L1", "API 可用性", true, 1),
			layer("L2", "协议兼容性", false, 0.8),
			layer("L3", "模型能力", true, 1),
			layer("L4", "稳定性", true, 0.9),
			layer("L5", "模型性能", true, 0.95),
			layer("L6", "参数边界与健壮性", true, 1),
		}, 0.8),
	}
}

func TestRenderMarkdown(t *testing.T) {
	r := buildFixtureReport()
	var buf bytes.Buffer
	if err := renderMarkdown(&buf, r); err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"# 模型接入体检报告",
		"## 体检结论",
		"接入与合规",
		"性能画像",
		"可用性冒烟",
		"## 关键信号",
		"TTFT P99",
		"疑似伪流式转发",
		"## 逐层明细",
		"## 原始指标附录",
		"## 裁判总结",
		"json_schema",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Markdown 缺少 %q\n---\n%s", want, out)
		}
	}

	// 总结 skipped（Summary 为 nil）
	if !strings.Contains(out, "未配置裁判模型") {
		t.Errorf("未配置 judge 时应输出跳过提示\n---\n%s", out)
	}
}

func TestRenderMarkdownSummaryStates(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r := buildFixtureReport()
		r.Summary = &types.JudgeSummary{Status: "ok", Text: "该模型可以接入。", Model: "judge-model"}
		var buf bytes.Buffer
		_ = renderMarkdown(&buf, r)
		if !strings.Contains(buf.String(), "该模型可以接入。") {
			t.Errorf("ok 状态应输出总结文本")
		}
	})
	t.Run("error", func(t *testing.T) {
		r := buildFixtureReport()
		r.Summary = &types.JudgeSummary{Status: "error", Error: "调用超时"}
		var buf bytes.Buffer
		_ = renderMarkdown(&buf, r)
		if !strings.Contains(buf.String(), "总结生成失败：调用超时") {
			t.Errorf("error 状态应输出失败原因")
		}
	})
}

func TestRenderConsole(t *testing.T) {
	r := buildFixtureReport()
	var buf bytes.Buffer
	renderConsole(&buf, r)
	out := buf.String()
	for _, want := range []string{"体检结论", "接入与合规", "性能画像", "可用性冒烟", "接入结论"} {
		if !strings.Contains(out, want) {
			t.Errorf("Console 缺少 %q\n---\n%s", want, out)
		}
	}
}

func TestVerdictLabel(t *testing.T) {
	tests := map[string]string{
		"pass":               "pass（接入与冒烟全部达标）",
		"pass_with_warnings": "pass_with_warnings（接入达标，冒烟存在短板）",
		"fail":               "fail（接入结论未通过）",
		"abort":              "abort（L1 门控未通过，已中止）",
		"no_layers_executed": "no_layers_executed（未执行任何层）",
		"unknown-verdict":    "unknown-verdict",
	}
	for in, want := range tests {
		if got := VerdictLabel(in); got != want {
			t.Errorf("VerdictLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
