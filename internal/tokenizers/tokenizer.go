// Package tokenizers 提供本地 token 计数能力，用于在供应商未返回 usage 时预估 token 数。
//
// 支持两种分发格式，由 New 自动探测：
//
//   - HuggingFace fast tokenizer：目录下的 tokenizer.json（BPE + ByteLevel）
//   - tiktoken：目录下的 tiktoken.model，配合 inspector.json 声明切分正则
//
// 实现全部为纯 Go，不依赖 CGO，可跨平台交叉编译。
//
// 现代 LLM 的切分正则普遍含有负向先行断言（如 \s+(?!\S)），Go 标准库 regexp
// 使用的 RE2 引擎不支持这类语法，因此本包在标准库无法编译某条正则时回退到
// dlclark/regexp2 回溯引擎。
package tokenizers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Encoding 是一次编码的结果。
//
// 字段命名与调用方既有用法保持一致：外部通过 len(enc.Tokens) 取 token 数。
type Encoding struct {
	IDs    []int    // token id 序列
	Tokens []string // 与 IDs 一一对应的 token 字面量
}

// encoder 是各分发格式的内部实现需要满足的接口。
type encoder interface {
	encode(text string) (*Encoding, error)
	// vocabSize 返回词表大小，仅用于诊断信息。
	vocabSize() int
}

// Tokenizer 是对外暴露的分词器句柄，可安全地被多个 goroutine 并发使用。
type Tokenizer struct {
	impl encoder
	name string
}

// EncodeSingle 编码单段文本。
//
// 不追加 BOS/EOS 等特殊 token，语义等价于 HuggingFace 的
// tokenizer.encode(text, add_special_tokens=False)。本包用于统计模型实际
// 生成的内容长度，补上模板特殊 token 只会引入与被测对象无关的偏差。
func (t *Tokenizer) EncodeSingle(text string) (enc *Encoding, err error) {
	if t == nil || t.impl == nil {
		return nil, errors.New("tokenizer: 未初始化")
	}
	// 词表数据来自外部文件，编码路径上的任何越界都不应该拖垮调用方进程。
	defer func() {
		if r := recover(); r != nil {
			enc, err = nil, fmt.Errorf("tokenizer: 编码 %q 时 panic: %v", t.name, r)
		}
	}()
	if text == "" {
		return &Encoding{}, nil
	}
	return t.impl.encode(text)
}

// Count 返回文本的 token 数，编码失败时返回 0，由调用方决定降级策略。
func (t *Tokenizer) Count(text string) int {
	enc, err := t.EncodeSingle(text)
	if err != nil || enc == nil {
		return 0
	}
	return len(enc.IDs)
}

// Name 返回分词器名称，通常是配置所在目录名。
func (t *Tokenizer) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

// VocabSize 返回词表大小，供诊断使用。
func (t *Tokenizer) VocabSize() int {
	if t == nil || t.impl == nil {
		return 0
	}
	return t.impl.vocabSize()
}

// cacheEntry 保证同一份配置只被解析一次：tokenizer.json 动辄数 MB，
// 而调用方（如 partialTokens）会在每次检查中重新取用分词器。
type cacheEntry struct {
	once sync.Once
	tk   *Tokenizer
	err  error
}

var cache sync.Map // map[string]*cacheEntry，key 为配置的绝对路径

// New 按路径加载分词器，结果按绝对路径缓存，重复调用不会重复解析。
//
// path 可以是：
//
//   - 分词器配置目录，例如 configs/tokenizers/deepseek-v4-flash-0731
//   - 目录下的具体文件，例如 .../tokenizer.json、.../tiktoken.model
//   - .../tokenizer_config.json —— 该文件仅含特殊 token 元数据，并非分词器
//     本体，此时自动回退到其所在目录继续探测
//
// 无论配置多么畸形，New 都返回 error 而不 panic。
func New(path string) (tk *Tokenizer, err error) {
	if path == "" {
		return nil, errors.New("tokenizer: 配置路径为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 解析路径 %q 失败: %w", path, err)
	}

	v, _ := cache.LoadOrStore(abs, &cacheEntry{})
	e := v.(*cacheEntry)
	e.once.Do(func() {
		e.tk, e.err = load(abs)
	})
	return e.tk, e.err
}

// Load 是 New 的别名，语义更贴合“加载一个分词器目录”的用法。
func Load(dir string) (*Tokenizer, error) { return New(dir) }

// load 完成一次真实的加载，把 panic 收敛成 error。
func load(abs string) (tk *Tokenizer, err error) {
	defer func() {
		if r := recover(); r != nil {
			tk, err = nil, fmt.Errorf("tokenizer: 加载 %q 时 panic: %v", abs, r)
		}
	}()

	dir, err := resolveDir(abs)
	if err != nil {
		return nil, err
	}

	spec, err := detect(dir)
	if err != nil {
		return nil, err
	}

	var impl encoder
	switch spec.Format {
	case formatHuggingFace:
		impl, err = newHFTokenizer(filepath.Join(dir, spec.VocabFile))
	case formatTiktoken:
		impl, err = newTiktokenTokenizer(dir, spec)
	default:
		return nil, fmt.Errorf("tokenizer: 未知格式 %q", spec.Format)
	}
	if err != nil {
		return nil, err
	}
	return &Tokenizer{impl: impl, name: filepath.Base(dir)}, nil
}

// resolveDir 把传入路径归一为分词器配置所在目录。
func resolveDir(abs string) (string, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("tokenizer: 读取 %q 失败: %w", abs, err)
	}
	if info.IsDir() {
		return abs, nil
	}
	return filepath.Dir(abs), nil
}

// 支持的分发格式。
const (
	formatHuggingFace = "huggingface"
	formatTiktoken    = "tiktoken"
)

// 目录中约定的文件名。
const (
	fileInspector     = "inspector.json"
	fileHFTokenizer   = "tokenizer.json"
	fileHFConfig      = "tokenizer_config.json"
	fileTiktokenVocab = "tiktoken.model"
)

// spec 描述一个分词器目录该如何加载。
//
// 绝大多数目录无需 inspector.json —— detect 能从文件名推断。仅 tiktoken 格式
// 必须显式提供 pattern：切分正则在官方分发里藏在 Python 源码中，运行时解析
// Python 既不可靠也无必要，因此由本项目在 inspector.json 中固化。
type spec struct {
	Format string `json:"format"`

	// VocabFile 是词表文件名，留空时按 Format 取默认值。
	VocabFile string `json:"vocab_file,omitempty"`

	// Pattern 是 tiktoken 格式的预切分正则。
	//
	// 注意：官方 Python 分发常用 Java 风格的字符类交集 [A&&[^B]]，需改写为
	// regexp2 支持的 .NET 字符类减法 [A-[B]] 后再填入此处，两者语义等价。
	Pattern string `json:"pattern,omitempty"`

	// SpecialTokensFile 指定从哪个文件读取特殊 token，默认 tokenizer_config.json
	// 的 added_tokens_decoder 字段。
	SpecialTokensFile string `json:"special_tokens_file,omitempty"`

	// ReservedSpecialTokens 是词表之后预留的特殊 token 槽位数量。tiktoken 系
	// 模型习惯预留固定数量的槽位，未在 added_tokens_decoder 中命名的槽位按
	// <|reserved_token_N|> 占位。
	ReservedSpecialTokens int `json:"reserved_special_tokens,omitempty"`
}

// detect 推断目录的分发格式：inspector.json 显式声明优先，其次按文件名推断。
func detect(dir string) (*spec, error) {
	if data, err := os.ReadFile(filepath.Join(dir, fileInspector)); err == nil {
		s := &spec{}
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("tokenizer: 解析 %s 失败: %w", fileInspector, err)
		}
		if s.Format == "" {
			return nil, fmt.Errorf("tokenizer: %s 缺少 format 字段", fileInspector)
		}
		applySpecDefaults(s)
		return s, nil
	}

	if exists(filepath.Join(dir, fileHFTokenizer)) {
		s := &spec{Format: formatHuggingFace}
		applySpecDefaults(s)
		return s, nil
	}

	if exists(filepath.Join(dir, fileTiktokenVocab)) {
		return nil, fmt.Errorf(
			"tokenizer: %s 是 tiktoken 格式，需在同目录提供 %s 声明切分正则（format/pattern）",
			dir, fileInspector)
	}

	hint := ""
	if exists(filepath.Join(dir, fileHFConfig)) {
		hint = fmt.Sprintf("；目录中只有 %s，该文件仅含特殊 token 元数据，不是分词器本体", fileHFConfig)
	}
	return nil, fmt.Errorf("tokenizer: %s 中找不到 %s 或 %s%s",
		dir, fileHFTokenizer, fileTiktokenVocab, hint)
}

func applySpecDefaults(s *spec) {
	if s.VocabFile == "" {
		switch s.Format {
		case formatHuggingFace:
			s.VocabFile = fileHFTokenizer
		case formatTiktoken:
			s.VocabFile = fileTiktokenVocab
		}
	}
	if s.SpecialTokensFile == "" {
		s.SpecialTokensFile = fileHFConfig
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
