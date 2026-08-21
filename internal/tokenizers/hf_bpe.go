package tokenizers

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
)

// bpePair 是一对相邻符号，作为合并表的键。用结构体而非拼接字符串做 key，
// 可以避免合并循环里每次查表都产生一次字符串分配。
type bpePair struct{ a, b string }

// bpe 实现 HuggingFace 的 BPE 模型。
//
// 合并顺序由 merges 列表的下标（rank）决定，每轮取 rank 最小的相邻对合并，
// 直到没有可合并的对为止。
type bpe struct {
	vocab map[string]int
	ranks map[bpePair]int

	unkToken     string
	hasUnk       bool
	fuseUnk      bool
	byteFallback bool

	continuingSubwordPrefix string
	endOfWordSuffix         string

	// cache 缓存片段到 token 序列的结果。自然语言里片段重复率极高，
	// 这一层能把长文本的合并开销压掉一个数量级。
	cache     sync.Map // map[string][]string
	cacheSize int64
	cacheMu   sync.Mutex
}

// bpeCacheLimit 限制缓存条目数，避免长时间运行时无界增长。
const bpeCacheLimit = 1 << 16

// tokenize 把一个已经过 ByteLevel 编码的片段切成 token 字面量序列。
func (m *bpe) tokenize(piece string) []string {
	if piece == "" {
		return nil
	}
	if v, ok := m.cache.Load(piece); ok {
		return v.([]string)
	}

	parts := m.initialSymbols(piece)
	for len(parts) > 1 {
		bestRank, bestIdx := math.MaxInt, -1
		for i := 0; i+1 < len(parts); i++ {
			if r, ok := m.ranks[bpePair{parts[i], parts[i+1]}]; ok && r < bestRank {
				bestRank, bestIdx = r, i
			}
		}
		if bestIdx < 0 {
			break
		}
		parts[bestIdx] += parts[bestIdx+1]
		parts = append(parts[:bestIdx+1], parts[bestIdx+2:]...)
	}

	m.storeCache(piece, parts)
	return parts
}

// initialSymbols 把片段拆成初始符号序列（每个 Unicode 字符一个符号），
// 并按模型配置补上 continuing_subword_prefix / end_of_word_suffix。
func (m *bpe) initialSymbols(piece string) []string {
	runes := []rune(piece)
	parts := make([]string, 0, len(runes))
	for i, r := range runes {
		s := string(r)
		if i > 0 && m.continuingSubwordPrefix != "" {
			s = m.continuingSubwordPrefix + s
		}
		if i == len(runes)-1 && m.endOfWordSuffix != "" {
			s += m.endOfWordSuffix
		}
		parts = append(parts, s)
	}
	return parts
}

func (m *bpe) storeCache(piece string, parts []string) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheSize >= bpeCacheLimit {
		return
	}
	if _, loaded := m.cache.LoadOrStore(piece, parts); !loaded {
		m.cacheSize++
	}
}

// lookup 把 token 字面量翻译成 id，并处理 byte_fallback 与 unk。
func (m *bpe) lookup(tokens []string, enc *Encoding) {
	prevWasUnk := false
	for _, tok := range tokens {
		if id, ok := m.vocab[tok]; ok {
			enc.IDs = append(enc.IDs, id)
			enc.Tokens = append(enc.Tokens, tok)
			prevWasUnk = false
			continue
		}

		// byte_fallback：词表里查不到的 token 退化成逐字节的 <0xXX>。
		if m.byteFallback {
			if ids, toks, ok := m.byteFallbackTokens(tok); ok {
				enc.IDs = append(enc.IDs, ids...)
				enc.Tokens = append(enc.Tokens, toks...)
				prevWasUnk = false
				continue
			}
		}

		if !m.hasUnk {
			// 既无 byte_fallback 也无 unk_token 时只能丢弃。对本包的用途
			// （统计长度）而言，少算一个 token 好过凭空造一个。
			continue
		}
		// fuse_unk：连续的未知 token 合并计为一个。
		if m.fuseUnk && prevWasUnk {
			continue
		}
		enc.IDs = append(enc.IDs, m.vocab[m.unkToken])
		enc.Tokens = append(enc.Tokens, m.unkToken)
		prevWasUnk = true
	}
}

func (m *bpe) byteFallbackTokens(tok string) ([]int, []string, bool) {
	// tok 此时仍是 ByteLevel 编码后的字符串，先还原成原始字节。
	raw := make([]byte, 0, len(tok))
	for _, r := range tok {
		b, ok := runeToByte[r]
		if !ok {
			return nil, nil, false
		}
		raw = append(raw, b)
	}

	ids := make([]int, 0, len(raw))
	toks := make([]string, 0, len(raw))
	for _, b := range raw {
		name := fmt.Sprintf("<0x%02X>", b)
		id, ok := m.vocab[name]
		if !ok {
			return nil, nil, false
		}
		ids = append(ids, id)
		toks = append(toks, name)
	}
	return ids, toks, true
}

// parseMerges 解析 merges 字段。
//
// tokenizer.json 有两种写法：早期版本是 "Ġ t" 这样的空格分隔字符串，
// 较新版本是 ["Ġ", "t"] 这样的二元数组。两者都要认。
//
// 这里刻意避开 []any —— merges 动辄十几万条，走 any 会多出成倍的分配。
func parseMerges(raw json.RawMessage) (map[bpePair]int, error) {
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil {
		ranks := make(map[bpePair]int, len(asStrings))
		for i, s := range asStrings {
			// 只按第一个空格切分：token 自身可能以空格结尾。
			a, b, ok := strings.Cut(s, " ")
			if !ok {
				return nil, fmt.Errorf("merges[%d] = %q 不是合法的合并规则", i, s)
			}
			putRank(ranks, bpePair{a, b}, i)
		}
		return ranks, nil
	}

	var asPairs [][]string
	if err := json.Unmarshal(raw, &asPairs); err == nil {
		ranks := make(map[bpePair]int, len(asPairs))
		for i, p := range asPairs {
			if len(p) != 2 {
				return nil, fmt.Errorf("merges[%d] 应当有 2 个元素，实际 %d 个", i, len(p))
			}
			putRank(ranks, bpePair{p[0], p[1]}, i)
		}
		return ranks, nil
	}

	return nil, fmt.Errorf("merges 既不是字符串数组也不是二元数组")
}

// putRank 只保留同一对首次出现的 rank，即优先级最高的那条规则。
func putRank(ranks map[bpePair]int, p bpePair, rank int) {
	if _, exists := ranks[p]; !exists {
		ranks[p] = rank
	}
}
