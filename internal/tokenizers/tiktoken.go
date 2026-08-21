package tokenizers

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkoukk/tiktoken-go"
)

// tiktokenTokenizer 加载 OpenAI tiktoken 格式的词表。
//
// 与 HuggingFace 的分发方式不同，tiktoken 只提供 "base64(token) rank" 的
// 纯文本词表，切分正则写死在各家的 Python 源码里。运行时解析 Python 既不
// 可靠也无必要，因此正则由本项目在同目录的 inspector.json 中固化。
type tiktokenTokenizer struct {
	tk      *tiktoken.Tiktoken
	decoder map[int]string
	size    int
}

func newTiktokenTokenizer(dir string, s *spec) (encoder, error) {
	if s.Pattern == "" {
		return nil, fmt.Errorf(
			"tokenizer: %s 缺少 pattern 字段；tiktoken 格式必须显式声明切分正则",
			filepath.Join(dir, fileInspector))
	}

	vocabPath := filepath.Join(dir, s.VocabFile)
	ranks, err := loadTiktokenBPE(vocabPath)
	if err != nil {
		return nil, err
	}

	special, err := loadSpecialTokens(filepath.Join(dir, s.SpecialTokensFile), len(ranks), s.ReservedSpecialTokens)
	if err != nil {
		return nil, err
	}

	core, err := tiktoken.NewCoreBPE(ranks, special, s.Pattern)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 构建 %s 的 BPE 失败: %w", dir, err)
	}

	enc := &tiktoken.Encoding{
		Name:           filepath.Base(dir),
		PatStr:         s.Pattern,
		MergeableRanks: ranks,
		SpecialTokens:  special,
	}

	decoder := make(map[int]string, len(ranks)+len(special))
	for tok, id := range ranks {
		decoder[id] = tok
	}
	for tok, id := range special {
		decoder[id] = tok
	}

	return &tiktokenTokenizer{
		tk:      tiktoken.NewTiktoken(core, enc, map[string]any{}),
		decoder: decoder,
		size:    len(ranks) + len(special),
	}, nil
}

func (t *tiktokenTokenizer) vocabSize() int { return t.size }

func (t *tiktokenTokenizer) encode(text string) (*Encoding, error) {
	// 不把特殊 token 字面量识别为特殊 token：本包统计的是模型生成的内容，
	// 内容里出现的 "[BOS]" 只是普通文本，不应折叠成一个控制 token。
	ids := t.tk.Encode(text, nil, nil)

	enc := &Encoding{IDs: ids, Tokens: make([]string, 0, len(ids))}
	for _, id := range ids {
		enc.Tokens = append(enc.Tokens, t.decoder[id])
	}
	return enc, nil
}

// loadTiktokenBPE 读取 "base64(token) rank" 逐行格式的词表。
func loadTiktokenBPE(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 读取词表 %s 失败: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	ranks := make(map[string]int, 1<<18)
	sc := bufio.NewScanner(f)
	// 单个 token 的 base64 不会很长，1MiB 的行缓冲足够，也能挡住畸形文件。
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		encoded, rankText, ok := strings.Cut(text, " ")
		if !ok {
			return nil, fmt.Errorf("tokenizer: %s 第 %d 行格式错误: %q", path, line, text)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("tokenizer: %s 第 %d 行 base64 解码失败: %w", path, line, err)
		}
		rank, err := strconv.Atoi(strings.TrimSpace(rankText))
		if err != nil {
			return nil, fmt.Errorf("tokenizer: %s 第 %d 行 rank 无效: %w", path, line, err)
		}
		ranks[string(raw)] = rank
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tokenizer: 扫描 %s 失败: %w", path, err)
	}
	if len(ranks) == 0 {
		return nil, fmt.Errorf("tokenizer: %s 中没有词表条目", path)
	}
	return ranks, nil
}

// loadSpecialTokens 从 tokenizer_config.json 的 added_tokens_decoder 读取特殊 token。
//
// tiktoken 系模型习惯在基础词表之后预留固定数量的槽位，其中只有一部分被命名。
// reserved 大于 0 时，未命名的槽位按 <|reserved_token_N|> 补齐，使词表规模与
// 官方实现一致。
func loadSpecialTokens(path string, base, reserved int) (map[string]int, error) {
	named := map[int]string{}

	if data, err := os.ReadFile(path); err == nil {
		var cfg struct {
			AddedTokensDecoder map[string]struct {
				Content string `json:"content"`
			} `json:"added_tokens_decoder"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("tokenizer: 解析 %s 失败: %w", path, err)
		}
		for idText, v := range cfg.AddedTokensDecoder {
			id, err := strconv.Atoi(idText)
			if err != nil {
				return nil, fmt.Errorf("tokenizer: %s 中的 token id %q 无效: %w", path, idText, err)
			}
			named[id] = v.Content
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("tokenizer: 读取 %s 失败: %w", path, err)
	}

	special := make(map[string]int, len(named)+reserved)
	for i := base; i < base+reserved; i++ {
		name, ok := named[i]
		if !ok {
			name = fmt.Sprintf("<|reserved_token_%d|>", i)
		}
		special[name] = i
	}
	// 落在预留区间之外的命名 token 也要收进来。
	for id, name := range named {
		if id < base || id >= base+reserved {
			special[name] = id
		}
	}
	return special, nil
}
