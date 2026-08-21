package tokenizers

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// hfTokenizer 加载 HuggingFace fast tokenizer 的 tokenizer.json。
//
// 编码流水线与 HuggingFace 保持一致：
//
//	added tokens 切分 → normalizer → pre_tokenizer → BPE → 查词表
//
// post_processor 被有意跳过：它的职责是套用对话模板并补 BOS/EOS，而本包
// 统计的是模型实际生成的内容长度，补上模板 token 只会引入无关偏差。
type hfTokenizer struct {
	normalizer   normalizer
	preTokenizer preTokenizer
	model        *bpe
	added        *addedVocab
	extraVocab   int // 落在基础词表之外的 added token 数量
}

// hfFile 是 tokenizer.json 中本包关心的字段。
type hfFile struct {
	AddedTokens  []hfAddedToken  `json:"added_tokens"`
	Normalizer   json.RawMessage `json:"normalizer"`
	PreTokenizer json.RawMessage `json:"pre_tokenizer"`
	Model        hfModel         `json:"model"`
}

type hfModel struct {
	Type                    string          `json:"type"`
	Vocab                   map[string]int  `json:"vocab"`
	Merges                  json.RawMessage `json:"merges"`
	UnkToken                *string         `json:"unk_token"`
	ContinuingSubwordPrefix *string         `json:"continuing_subword_prefix"`
	EndOfWordSuffix         *string         `json:"end_of_word_suffix"`
	FuseUnk                 bool            `json:"fuse_unk"`
	ByteFallback            bool            `json:"byte_fallback"`
}

type hfAddedToken struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
	LStrip  bool   `json:"lstrip"`
	RStrip  bool   `json:"rstrip"`
	Special bool   `json:"special"`
}

func newHFTokenizer(path string) (encoder, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 读取 %s 失败: %w", path, err)
	}

	var f hfFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("tokenizer: 解析 %s 失败: %w", path, err)
	}

	// model.type 缺省视作 BPE，与 HuggingFace 的行为一致。
	if f.Model.Type != "" && f.Model.Type != "BPE" {
		return nil, fmt.Errorf(
			"tokenizer: %s 的模型类型是 %q，本包目前只实现了 BPE",
			path, f.Model.Type)
	}
	if len(f.Model.Vocab) == 0 {
		return nil, fmt.Errorf(
			"tokenizer: %s 中没有词表；若这是 %s，它只含特殊 token 元数据，请改用 %s",
			path, fileHFConfig, fileHFTokenizer)
	}

	ranks, err := parseMerges(f.Model.Merges)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: 解析 %s 的 merges 失败: %w", path, err)
	}

	nrm, err := buildNormalizer(f.Normalizer)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: %s: %w", path, err)
	}
	pre, err := buildPreTokenizer(f.PreTokenizer)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: %s: %w", path, err)
	}

	model := &bpe{
		vocab:        f.Model.Vocab,
		ranks:        ranks,
		fuseUnk:      f.Model.FuseUnk,
		byteFallback: f.Model.ByteFallback,
	}
	if f.Model.UnkToken != nil {
		if _, ok := f.Model.Vocab[*f.Model.UnkToken]; ok {
			model.unkToken, model.hasUnk = *f.Model.UnkToken, true
		}
	}
	if f.Model.ContinuingSubwordPrefix != nil {
		model.continuingSubwordPrefix = *f.Model.ContinuingSubwordPrefix
	}
	if f.Model.EndOfWordSuffix != nil {
		model.endOfWordSuffix = *f.Model.EndOfWordSuffix
	}

	added, err := newAddedVocab(f.AddedTokens)
	if err != nil {
		return nil, fmt.Errorf("tokenizer: %s: %w", path, err)
	}

	extra := 0
	for _, t := range f.AddedTokens {
		if _, ok := f.Model.Vocab[t.Content]; !ok {
			extra++
		}
	}

	return &hfTokenizer{
		normalizer:   nrm,
		preTokenizer: pre,
		model:        model,
		added:        added,
		extraVocab:   extra,
	}, nil
}

func (t *hfTokenizer) vocabSize() int { return len(t.model.vocab) + t.extraVocab }

func (t *hfTokenizer) encode(text string) (*Encoding, error) {
	enc := &Encoding{}
	for _, seg := range t.added.split(text) {
		if seg.added != nil {
			enc.IDs = append(enc.IDs, seg.added.ID)
			enc.Tokens = append(enc.Tokens, seg.added.Content)
			continue
		}
		if err := t.encodePlain(seg.text, enc); err != nil {
			return nil, err
		}
	}
	return enc, nil
}

// encodePlain 编码一段不含 added token 的普通文本。
func (t *hfTokenizer) encodePlain(s string, enc *Encoding) error {
	if s == "" {
		return nil
	}
	if t.normalizer != nil {
		s = t.normalizer.normalize(s)
	}

	pieces := []string{s}
	if t.preTokenizer != nil {
		var err error
		if pieces, err = t.preTokenizer.apply(pieces); err != nil {
			return fmt.Errorf("tokenizer: 预切分失败: %w", err)
		}
	}

	for _, p := range pieces {
		t.model.lookup(t.model.tokenize(p), enc)
	}
	return nil
}

// addedVocab 负责在正常分词之前把 added token 原样切出来。
//
// 这些 token（如 <｜begin▁of▁sentence｜>）必须整体成为一个 token，不能交给
// BPE 拆分，因此要先于流水线的其余步骤匹配。
type addedVocab struct {
	re     *regexp.Regexp
	byText map[string]*hfAddedToken
	tokens []hfAddedToken
}

// addedSegment 要么是一个命中的 added token，要么是一段待正常编码的文本。
type addedSegment struct {
	text  string
	added *hfAddedToken
}

func newAddedVocab(tokens []hfAddedToken) (*addedVocab, error) {
	av := &addedVocab{
		byText: make(map[string]*hfAddedToken, len(tokens)),
		tokens: append([]hfAddedToken(nil), tokens...),
	}
	if len(av.tokens) == 0 {
		return av, nil
	}

	// 按内容长度降序排列：Go 的正则交替是最左优先而非最长优先，
	// 长的排在前面才能保证 "<|ab|>" 不会被 "<|a|>" 抢先匹配掉。
	order := make([]int, len(av.tokens))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return len(av.tokens[order[i]].Content) > len(av.tokens[order[j]].Content)
	})

	alts := make([]string, 0, len(order))
	for _, i := range order {
		tk := &av.tokens[i]
		if tk.Content == "" {
			continue
		}
		av.byText[tk.Content] = tk

		// 全部按字面量转义：added token 里的 <、|、? 等字符不该被当成正则元字符。
		expr := regexp.QuoteMeta(tk.Content)
		if tk.LStrip {
			expr = `\s*` + expr
		}
		if tk.RStrip {
			expr += `\s*`
		}
		alts = append(alts, expr)
	}
	if len(alts) == 0 {
		return av, nil
	}

	// 这里只有字面量和 \s*，标准库足以胜任，无需回溯引擎。
	re, err := regexp.Compile(strings.Join(alts, "|"))
	if err != nil {
		return nil, fmt.Errorf("构建 added token 匹配表失败: %w", err)
	}
	av.re = re
	return av, nil
}

// split 把文本切成 added token 与普通文本交替的片段序列。
func (av *addedVocab) split(text string) []addedSegment {
	if av.re == nil || text == "" {
		return []addedSegment{{text: text}}
	}

	matches := av.re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []addedSegment{{text: text}}
	}

	segs := make([]addedSegment, 0, len(matches)*2+1)
	prev := 0
	for _, m := range matches {
		matched := text[m[0]:m[1]]
		tk, ok := av.byText[matched]
		if !ok {
			// lstrip/rstrip 会让匹配串带上额外空白，去掉后再查一次。
			tk, ok = av.byText[strings.TrimSpace(matched)]
		}
		if !ok {
			// 理论上不可达；真出现了就当普通文本处理，不要吞掉内容。
			continue
		}
		if m[0] > prev {
			segs = append(segs, addedSegment{text: text[prev:m[0]]})
		}
		segs = append(segs, addedSegment{added: tk})
		prev = m[1]
	}
	if prev < len(text) {
		segs = append(segs, addedSegment{text: text[prev:]})
	}
	return segs
}
