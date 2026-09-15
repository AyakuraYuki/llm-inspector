package samplelog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/performance/internal/types"
)

// resetForTest 让每个用例从干净状态开始，并在结束时释放文件句柄。
func resetForTest(t *testing.T, path string) {
	t.Helper()
	Init(path)
	t.Cleanup(Close)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开样本文件失败: %v", err)
	}
	defer func() { _ = f.Close() }()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			lines = append(lines, sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("读取样本文件失败: %v", err)
	}
	return lines
}

func TestWriteLevel_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	resetForTest(t, path)

	ts := time.Date(2026, 9, 15, 10, 30, 0, 0, time.UTC)
	ok := types.RequestMetrics{
		Timestamp:    ts,
		TTFT:         120 * time.Millisecond,
		TotalLatency: 2 * time.Second,
		InputTokens:  1000,
		OutputTokens: 200,
		ITLSamplesMS: []float64{9.5, 10.5},
		Success:      true,
	}
	failed := types.RequestMetrics{
		Timestamp:    ts.Add(time.Second),
		TotalLatency: 50 * time.Millisecond,
		Success:      false,
		ErrorType:    types.ErrorTypeRateLimit,
		Error:        "HTTP 429: slow down",
		RequestID:    "req-abc",
	}

	lv := LevelContext{
		Model:       "gpt-5.6-sol",
		Provider:    types.ProviderOpenAI,
		TokenGroup:  "openai-channel",
		Concurrency: 50,
		LevelStart:  ts,
	}
	WriteLevel(lv, []types.RequestMetrics{ok, failed})
	Close()

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("样本行数 = %d, want 2（成功与失败样本都应落盘）", len(lines))
	}
	if got := Count(); got != 2 {
		t.Errorf("Count() = %d, want 2", got)
	}

	var first sampleLine
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("反序列化第一行失败: %v", err)
	}
	if first.Model != "gpt-5.6-sol" || first.Concurrency != 50 {
		t.Errorf("档位标识 = %q/%d, want gpt-5.6-sol/50", first.Model, first.Concurrency)
	}
	if !first.Success {
		t.Error("first.Success = false, want true")
	}
	if first.TTFTMs != 120 {
		t.Errorf("TTFTMs = %v, want 120", first.TTFTMs)
	}
	if first.E2EMs != 2000 {
		t.Errorf("E2EMs = %v, want 2000", first.E2EMs)
	}
	if len(first.ITLMs) != 2 {
		t.Errorf("ITLMs = %v, want 2 个样本（ITL 必须留档，否则事后无法重算 ITL 分布）", first.ITLMs)
	}

	var second sampleLine
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("反序列化第二行失败: %v", err)
	}
	if second.Success {
		t.Error("second.Success = true, want false")
	}
	if second.ErrorType != string(types.ErrorTypeRateLimit) {
		t.Errorf("ErrorType = %q, want %q", second.ErrorType, types.ErrorTypeRateLimit)
	}
	if second.RequestID != "req-abc" {
		t.Errorf("RequestID = %q, want req-abc", second.RequestID)
	}
}

func TestWriteLevel_DisabledCreatesNoFile(t *testing.T) {
	dir := t.TempDir()
	resetForTest(t, "") // 未配置 sample_output

	WriteLevel(LevelContext{Model: "m"}, []types.RequestMetrics{{Success: true}})
	Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("未开启导出时不应创建任何文件，实际有 %d 个", len(entries))
	}
	if got := Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}
	if Enabled() {
		t.Error("Enabled() = true, want false")
	}
}

func TestWriteLevel_AppendsAcrossLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "samples.jsonl")
	resetForTest(t, path) // 父目录不存在，应被自动创建

	s := []types.RequestMetrics{{TotalLatency: time.Second, Success: true}}
	WriteLevel(LevelContext{Model: "m", Concurrency: 10}, s)
	WriteLevel(LevelContext{Model: "m", Concurrency: 20}, s)
	Close()

	if lines := readLines(t, path); len(lines) != 2 {
		t.Fatalf("样本行数 = %d, want 2（逐档位追加，不覆盖）", len(lines))
	}
}

func TestWriteLevel_EmptySamplesNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	resetForTest(t, path)

	WriteLevel(LevelContext{Model: "m"}, nil)
	Close()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("空样本不应创建文件（避免留下空文件）")
	}
}
