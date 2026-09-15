package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDynamicPrompt_NoCounterFallsBackToChars(t *testing.T) {
	p := BuildDynamicPrompt(500, nil)
	if !strings.HasPrefix(p, "[bench-nonce ") {
		t.Fatalf("动态 prompt 应以 nonce 开头，got %q", p[:min(40, len(p))])
	}
	if !strings.HasSuffix(p, dynamicPromptSuffix) {
		t.Fatalf("动态 prompt 应以固定指令结尾")
	}
	// 字符近似：长度不小于 target*4 减去后缀
	if len(p) < 500*approxCharsPerToken {
		t.Errorf("字符近似模式下长度 %d 小于目标 %d", len(p), 500*approxCharsPerToken)
	}
}

func TestBuildDynamicPrompt_ConvergesWithRealTokenizer(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "configs", "tokenizers", "kimi-k3")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("词表目录不存在，跳过：%v", err)
	}
	ctr := CounterFor(dir)
	if ctr == nil {
		t.Fatalf("CounterFor(%s) 返回 nil", dir)
	}
	if ctr.Fingerprint() == "" {
		t.Errorf("Fingerprint 不应为空")
	}

	for _, target := range []int{64, 500, 2000, 6000} {
		p := BuildDynamicPrompt(target, ctr)
		got := ctr.Count(p)
		// 允许 1% 或 3 个 token 的偏差：段落边界处 BPE 合并可能差 ±1，
		// 多段累加上限取 3；这已远优于字符近似的 ±30%。
		tol := max(3, target/100)
		if got < target-tol || got > target+tol {
			t.Errorf("target=%d: 实际 %d tokens，超出容差 ±%d", target, got, tol)
		}
	}
}

func TestBuildDynamicPrompt_TwoCallsDiffer(t *testing.T) {
	a := BuildDynamicPrompt(300, nil)
	b := BuildDynamicPrompt(300, nil)
	if a == b {
		t.Errorf("两次动态 prompt 完全相同，nonce/乱序未生效")
	}
}

func TestPickTargetTokens(t *testing.T) {
	if got := PickTargetTokens(0, 0, 2000); got != 2000 {
		t.Errorf("未配置区间应返回 fallback 2000，got %d", got)
	}
	if got := PickTargetTokens(100, 0, 2000); got != 2000 {
		t.Errorf("区间只配一半应返回 fallback，got %d", got)
	}
	for range 200 {
		got := PickTargetTokens(100, 200, 2000)
		if got < 100 || got > 200 {
			t.Fatalf("采样值 %d 超出 [100, 200]", got)
		}
	}
	// 区间反写也能工作
	if got := PickTargetTokens(50, 50, 0); got != 50 {
		t.Errorf("退化区间 [50,50] 应恒为 50，got %d", got)
	}
}

func TestLoadDatasetLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompts.txt")
	content := "  first prompt  \n\n\nsecond prompt\n   \nthird\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := LoadDatasetLines(path)
	if err != nil {
		t.Fatalf("LoadDatasetLines: %v", err)
	}
	want := []string{"first prompt", "second prompt", "third"}
	if len(lines) != len(want) {
		t.Fatalf("行数 %d, want %d: %v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
	for range 50 {
		if got := PickDatasetLine(lines); got != "first prompt" && got != "second prompt" && got != "third" {
			t.Fatalf("PickDatasetLine 返回了数据集之外的内容 %q", got)
		}
	}
}

func TestLoadDatasetLines_EmptyFileRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte("\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDatasetLines(path); err == nil {
		t.Fatalf("全空数据集应报错")
	}
	if _, err := LoadDatasetLines(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatalf("不存在的数据集应报错")
	}
}

func TestValidateTokenizer(t *testing.T) {
	if err := ValidateTokenizer("", "whatever"); err != nil {
		t.Errorf("未配置路径应恒为 nil，got %v", err)
	}
	if err := ValidateTokenizer(t.TempDir(), ""); err == nil {
		t.Errorf("空目录不是合法词表，应报错")
	}
	dir := filepath.Join("..", "..", "..", "..", "configs", "tokenizers", "kimi-k3")
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Skipf("词表目录不存在，跳过：%v", statErr)
	}
	if err := ValidateTokenizer(dir, ""); err != nil {
		t.Fatalf("真实词表应可加载：%v", err)
	}
	fp := CounterFor(dir).Fingerprint()
	if err := ValidateTokenizer(dir, fp); err != nil {
		t.Errorf("指纹一致应通过：%v", err)
	}
	if err := ValidateTokenizer(dir, "deadbeef"); err == nil {
		t.Errorf("指纹不一致应报错")
	}
}
