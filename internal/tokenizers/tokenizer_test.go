package tokenizers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dlclark/regexp2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configRoot = "../../configs/tokenizers"

// goldenCase 是一条对拍用例，由 testdata/gen_*.py 用官方 Python 实现生成。
type goldenCase struct {
	Text  string `json:"text"`
	IDs   []int  `json:"ids"`
	Count int    `json:"count"`
}

func loadGolden(t *testing.T, name string) []goldenCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err, "读取 golden 文件失败")
	var cases []goldenCase
	require.NoError(t, json.Unmarshal(data, &cases), "解析 golden 文件失败")
	require.NotEmpty(t, cases)
	return cases
}

// requireConfig 跳过缺少配置文件的环境，而不是把测试判为失败。
func requireConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(configRoot, dir)
	if _, err := os.Stat(path); err != nil {
		t.Skipf("缺少分词器配置 %s，跳过", path)
	}
	return path
}

// runGolden 逐条比对 id 序列。只比 token 数不足以证明实现正确 ——
// 数量相同而切分位置不同的情况完全可能出现。
func runGolden(t *testing.T, dir, golden string) {
	t.Helper()
	path := requireConfig(t, dir)
	cases := loadGolden(t, golden)

	tk, err := New(path)
	require.NoError(t, err)
	require.NotNil(t, tk)

	mismatch := 0
	for i, c := range cases {
		enc, err := tk.EncodeSingle(c.Text)
		if !assert.NoError(t, err, "用例 %d 编码失败: %q", i, c.Text) {
			continue
		}
		// 归一化 nil 与空切片：两者都表示零个 token。
		got := enc.IDs
		if got == nil {
			got = []int{}
		}
		want := c.IDs
		if want == nil {
			want = []int{}
		}
		if !assert.Equal(t, want, got, "用例 %d 的 id 序列不一致: %q", i, c.Text) {
			mismatch++
			t.Logf("  期望 %d tokens: %v", c.Count, c.IDs)
			t.Logf("  实际 %d tokens: %v", len(enc.IDs), enc.IDs)
			t.Logf("  实际切分: %q", enc.Tokens)
		}
		assert.Equal(t, c.Count, tk.Count(c.Text), "用例 %d 的 Count 不一致", i)
	}
	t.Logf("%s: %d/%d 用例通过，词表规模 %d", dir, len(cases)-mismatch, len(cases), tk.VocabSize())
}

// TestHuggingFaceGolden 对拍 HuggingFace transformers 的输出。
func TestHuggingFaceGolden(t *testing.T) {
	runGolden(t, "deepseek-v4", "deepseek.golden.json")
}

// TestTiktokenGolden 对拍官方 tiktoken 库的输出。
func TestTiktokenGolden(t *testing.T) {
	runGolden(t, "kimi-k3", "kimi.golden.json")
}

// TestDeepSeekV4Pro 确认第二份 DeepSeek 配置同样可用。
func TestDeepSeekV4Pro(t *testing.T) {
	path := requireConfig(t, "deepseek-v4")
	tk, err := New(path)
	require.NoError(t, err)

	enc, err := tk.EncodeSingle("Just say hi to everyone.")
	require.NoError(t, err)
	assert.Len(t, enc.IDs, 6)
	assert.Len(t, enc.Tokens, 6)
}

// TestNewAcceptsFilePaths 验证 New 既接受目录也接受目录下的具体文件。
func TestNewAcceptsFilePaths(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")

	for _, path := range []string{
		dir,
		filepath.Join(dir, fileHFTokenizer),
		// tokenizer_config.json 只有元数据，不是分词器本体；
		// New 应当回退到所在目录继续探测，而不是失败。
		filepath.Join(dir, fileHFConfig),
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			tk, err := New(path)
			require.NoError(t, err)
			assert.Equal(t, 6, tk.Count("Just say hi to everyone."))
		})
	}
}

// TestNewCaches 验证同一路径不会被重复解析。
func TestNewCaches(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	a, err := New(dir)
	require.NoError(t, err)
	b, err := New(dir)
	require.NoError(t, err)
	assert.Same(t, a, b, "同一路径应复用缓存实例")
}

// TestNewErrors 验证所有失败路径都返回 error 而不是 panic。
//
// 这是替换掉旧实现的主要动因之一：调用方（partialTokens）依赖 err 判断来
// 决定是否降级到字符数估算，一旦底层 panic，那条降级路径就永远走不到，
// 整个评测进程会直接崩掉。
func TestNewErrors(t *testing.T) {
	tmp := t.TempDir()

	emptyDir := filepath.Join(tmp, "empty")
	require.NoError(t, os.Mkdir(emptyDir, 0o755))

	// 只有 tokenizer_config.json 的目录：这正是原先触发 panic 的输入。
	onlyConfig := filepath.Join(tmp, "only-config")
	require.NoError(t, os.Mkdir(onlyConfig, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(onlyConfig, fileHFConfig),
		[]byte(`{"tokenizer_class":"PreTrainedTokenizerFast"}`), 0o644))

	brokenJSON := filepath.Join(tmp, "broken")
	require.NoError(t, os.Mkdir(brokenJSON, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(brokenJSON, fileHFTokenizer),
		[]byte(`{not json`), 0o644))

	noVocab := filepath.Join(tmp, "no-vocab")
	require.NoError(t, os.Mkdir(noVocab, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(noVocab, fileHFTokenizer),
		[]byte(`{"model":{"type":"BPE","vocab":{},"merges":[]}}`), 0o644))

	unigram := filepath.Join(tmp, "unigram")
	require.NoError(t, os.Mkdir(unigram, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(unigram, fileHFTokenizer),
		[]byte(`{"model":{"type":"Unigram","vocab":{"a":1},"merges":[]}}`), 0o644))

	tiktokenNoSpec := filepath.Join(tmp, "tiktoken-no-spec")
	require.NoError(t, os.Mkdir(tiktokenNoSpec, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tiktokenNoSpec, fileTiktokenVocab),
		[]byte("IQ== 0\n"), 0o644))

	cases := []struct {
		name    string
		path    string
		wantMsg string
	}{
		{"空路径", "", "配置路径为空"},
		{"不存在的路径", filepath.Join(tmp, "nope"), "失败"},
		{"空目录", emptyDir, "找不到"},
		{"只有 tokenizer_config.json", onlyConfig, fileHFConfig},
		{"tokenizer.json 不是合法 JSON", brokenJSON, "解析"},
		{"词表为空", noVocab, "没有词表"},
		{"非 BPE 模型", unigram, "只实现了 BPE"},
		{"tiktoken 缺少 inspector.json", tiktokenNoSpec, fileInspector},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk, err := New(c.path)
			require.Error(t, err, "应当返回 error")
			assert.Nil(t, tk)
			assert.Contains(t, err.Error(), c.wantMsg)
		})
	}
}

// TestNilTokenizer 验证零值与 nil 句柄不会 panic。
func TestNilTokenizer(t *testing.T) {
	var tk *Tokenizer
	assert.Equal(t, 0, tk.Count("hello"))
	assert.Equal(t, "", tk.Name())
	assert.Equal(t, 0, tk.VocabSize())
	_, err := tk.EncodeSingle("hello")
	assert.Error(t, err)

	empty := &Tokenizer{}
	_, err = empty.EncodeSingle("hello")
	assert.Error(t, err)
}

// TestEmptyText 空串编码为零个 token，与 HuggingFace 一致。
func TestEmptyText(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	tk, err := New(dir)
	require.NoError(t, err)

	enc, err := tk.EncodeSingle("")
	require.NoError(t, err)
	assert.Empty(t, enc.IDs)
	assert.Equal(t, 0, tk.Count(""))
}

// TestAddedTokensAreAtomic 验证 added token 整体成为一个 token，不被 BPE 拆开。
func TestAddedTokensAreAtomic(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	tk, err := New(dir)
	require.NoError(t, err)

	const bos = "<｜begin▁of▁sentence｜>"
	enc, err := tk.EncodeSingle(bos)
	require.NoError(t, err)
	require.Len(t, enc.IDs, 1, "特殊 token 应当整体编码")
	assert.Equal(t, 0, enc.IDs[0])
	assert.Equal(t, bos, enc.Tokens[0])
}

// TestPatternFallback 验证含负向先行断言的正则会回退到回溯引擎。
//
// \s+(?!\S) 出现在几乎所有现代 LLM 的切分正则里，而 Go 标准库的 RE2 引擎
// 不支持这类语法 —— 旧实现正是在这里 panic 的。
func TestPatternFallback(t *testing.T) {
	plain, err := compilePattern(`\p{N}{1,3}`)
	require.NoError(t, err)
	assert.NotNil(t, plain.std, "无断言的正则应当走标准库")
	assert.Nil(t, plain.alt)

	lookahead, err := compilePattern(`\s+(?!\S)|\s+`)
	require.NoError(t, err)
	assert.Nil(t, lookahead.std, "标准库无法编译含先行断言的正则")
	assert.NotNil(t, lookahead.alt, "应当回退到 regexp2")

	_, err = compilePattern(`(?!`)
	assert.Error(t, err, "两个引擎都编译不了时应返回 error")
}

// TestPatternFindAllAgreement 验证两条引擎路径在同一输入上给出相同的匹配区间。
//
// 优先用标准库是性能优化，这条测试守住它不改变语义。
func TestPatternFindAllAgreement(t *testing.T) {
	const expr = `\p{N}{1,3}|[a-z]+`
	inputs := []string{
		"abc123def456",
		"你好 abc 123",
		"1234567",
		"",
		"no digits here",
		"混合text123中文456",
	}

	std, err := compilePattern(expr)
	require.NoError(t, err)
	require.NotNil(t, std.std, "该正则应当能被标准库编译")

	// 手工构造一个只走 regexp2 的等价 pattern。
	re, err := regexp2.Compile(expr, regexp2.None)
	require.NoError(t, err)
	re.MatchTimeout = regexpTimeout
	altOnly := &pattern{alt: re}

	for _, in := range inputs {
		assert.Equal(t, std.findAll(in), altOnly.findAll(in),
			"两条引擎路径在 %q 上的匹配区间应当一致", in)
	}
}

// TestSegmentizeAssemble 覆盖 Split 的各种分隔符归属策略。
func TestSegmentizeAssemble(t *testing.T) {
	re, err := compilePattern(`\p{N}{1,3}`)
	require.NoError(t, err)

	const input = "abc123def"
	segs := segmentize(input, re.findAll(input), false)

	cases := map[string][]string{
		behaviorIsolated:           {"abc", "123", "def"},
		behaviorRemoved:            {"abc", "def"},
		behaviorMergedWithPrevious: {"abc123", "def"},
		behaviorMergedWithNext:     {"abc", "123def"},
		behaviorContiguous:         {"abc", "123", "def"},
	}
	for behavior, want := range cases {
		t.Run(behavior, func(t *testing.T) {
			assert.Equal(t, want, assemble(segs, behavior))
		})
	}
}

// TestSegmentizeInvert 验证 invert 会互换匹配与非匹配的身份。
func TestSegmentizeInvert(t *testing.T) {
	re, err := compilePattern(`\p{N}+`)
	require.NoError(t, err)

	const input = "abc123def"
	spans := re.findAll(input)

	assert.Equal(t, []string{"abc", "def"},
		assemble(segmentize(input, spans, false), behaviorRemoved),
		"正常模式下移除数字")
	assert.Equal(t, []string{"123"},
		assemble(segmentize(input, spans, true), behaviorRemoved),
		"invert 后移除非数字")
}

// TestContiguousMergesRuns 验证 Contiguous 会把连续命中合并成一段。
func TestContiguousMergesRuns(t *testing.T) {
	re, err := compilePattern(`\p{N}`)
	require.NoError(t, err)

	const input = "a123b"
	segs := segmentize(input, re.findAll(input), false)
	assert.Equal(t, []string{"a", "1", "2", "3", "b"}, assemble(segs, behaviorIsolated))
	assert.Equal(t, []string{"a", "123", "b"}, assemble(segs, behaviorContiguous))
}

// TestByteLevelRoundTrip 验证字节映射表覆盖全部 256 个字节且可逆。
func TestByteLevelRoundTrip(t *testing.T) {
	seen := make(map[rune]bool, 256)
	for b := range 256 {
		r := byteToRune[b]
		require.False(t, seen[r], "字节 %d 的映射目标 %q 与其他字节冲突", b, r)
		seen[r] = true
		back, ok := runeToByte[r]
		require.True(t, ok, "字节 %d 的映射缺少反查项", b)
		assert.Equal(t, byte(b), back)
	}
	assert.Len(t, seen, 256)

	// 空格映射为 Ġ 是 GPT-2 系词表最容易验证的标志。
	assert.Equal(t, "Ġ", string(byteToRune[' ']))
	assert.Equal(t, "Ġhello", byteLevelEncode(" hello"))
}

// TestParseMerges 覆盖 merges 的两种序列化写法。
func TestParseMerges(t *testing.T) {
	t.Run("字符串数组", func(t *testing.T) {
		ranks, err := parseMerges(json.RawMessage(`["Ġ t","Ġ a","h e"]`))
		require.NoError(t, err)
		assert.Equal(t, 0, ranks[bpePair{"Ġ", "t"}])
		assert.Equal(t, 1, ranks[bpePair{"Ġ", "a"}])
		assert.Equal(t, 2, ranks[bpePair{"h", "e"}])
	})

	t.Run("二元数组", func(t *testing.T) {
		ranks, err := parseMerges(json.RawMessage(`[["Ġ","t"],["h","e"]]`))
		require.NoError(t, err)
		assert.Equal(t, 0, ranks[bpePair{"Ġ", "t"}])
		assert.Equal(t, 1, ranks[bpePair{"h", "e"}])
	})

	t.Run("token 自身含空格", func(t *testing.T) {
		// 只按第一个空格切分，右侧的空格属于 token 本身。
		ranks, err := parseMerges(json.RawMessage(`["a b c"]`))
		require.NoError(t, err)
		assert.Equal(t, 0, ranks[bpePair{"a", "b c"}])
	})

	t.Run("重复规则保留最高优先级", func(t *testing.T) {
		ranks, err := parseMerges(json.RawMessage(`["a b","c d","a b"]`))
		require.NoError(t, err)
		assert.Equal(t, 0, ranks[bpePair{"a", "b"}])
	})

	t.Run("非法输入", func(t *testing.T) {
		_, err := parseMerges(json.RawMessage(`["nospace"]`))
		assert.Error(t, err)
		_, err = parseMerges(json.RawMessage(`[["a"]]`))
		assert.Error(t, err)
		_, err = parseMerges(json.RawMessage(`{"a":1}`))
		assert.Error(t, err)
	})
}

// TestBPECacheIsConsistent 验证缓存命中与未命中给出相同结果。
func TestBPECacheIsConsistent(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	tk, err := New(dir)
	require.NoError(t, err)

	const text = "repeat repeat repeat 重复 重复 重复"
	first, err := tk.EncodeSingle(text)
	require.NoError(t, err)
	second, err := tk.EncodeSingle(text)
	require.NoError(t, err)
	assert.Equal(t, first.IDs, second.IDs)
}

// TestConcurrentEncode 验证分词器可被并发使用。
func TestConcurrentEncode(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	tk, err := New(dir)
	require.NoError(t, err)

	const text = "并发编码测试 concurrent encoding test 12345"
	want := tk.Count(text)
	require.Positive(t, want)

	const workers = 16
	errs := make(chan error, workers)
	for i := range workers {
		go func(i int) {
			// 混入独占文本，让每个 goroutine 都真实写一次 BPE 缓存。
			own := fmt.Sprintf("%s goroutine-%d", text, i)
			if n := tk.Count(own); n <= want {
				errs <- fmt.Errorf("goroutine %d: 期望多于 %d 个 token，实际 %d", i, want, n)
				return
			}
			if n := tk.Count(text); n != want {
				errs <- fmt.Errorf("goroutine %d: 期望 %d 个 token，实际 %d", i, want, n)
				return
			}
			errs <- nil
		}(i)
	}
	for range workers {
		require.NoError(t, <-errs)
	}
}

// TestLongTextDoesNotBlowUp 用超长文本探一遍回溯引擎的超时保护。
func TestLongTextDoesNotBlowUp(t *testing.T) {
	dir := requireConfig(t, "deepseek-v4")
	tk, err := New(dir)
	require.NoError(t, err)

	long := strings.Repeat("这是一段用于压力测试的中文文本，混入 English words 和 12345 数字。\n", 500)
	n := tk.Count(long)
	assert.Positive(t, n)
	t.Logf("%d 字符 -> %d tokens", len([]rune(long)), n)
}

func BenchmarkEncodeHuggingFace(b *testing.B) {
	tk, err := New(filepath.Join(configRoot, "deepseek-v4"))
	if err != nil {
		b.Skipf("缺少分词器配置: %v", err)
	}
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. 敏捷的棕色狐狸跳过了懒狗。\n", 20)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if tk.Count(text) == 0 {
			b.Fatal("编码结果为空")
		}
	}
}

func BenchmarkEncodeTiktoken(b *testing.B) {
	tk, err := New(filepath.Join(configRoot, "kimi-k3"))
	if err != nil {
		b.Skipf("缺少分词器配置: %v", err)
	}
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. 敏捷的棕色狐狸跳过了懒狗。\n", 20)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		if tk.Count(text) == 0 {
			b.Fatal("编码结果为空")
		}
	}
}
