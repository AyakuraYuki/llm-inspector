package tokenizers

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// preTokenizer 把文本切成待做 BPE 的片段。
//
// 流水线以片段列表为单位逐级传递：每一级对上一级的每个片段独立施加规则，
// 再把结果展平。这与 HuggingFace 的 PreTokenizedString 语义一致，也是
// Sequence 型 pre_tokenizer 必须逐级施加、而不能把多条正则合并成一条
// alternation 的原因 —— 前面的规则会改变后面规则看到的输入边界。
type preTokenizer interface {
	apply(pieces []string) ([]string, error)
}

func buildPreTokenizer(raw json.RawMessage) (preTokenizer, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("解析 pre_tokenizer 失败: %w", err)
	}

	switch head.Type {
	case "Sequence":
		var v struct {
			PreTokenizers []json.RawMessage `json:"pretokenizers"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Sequence pre_tokenizer 失败: %w", err)
		}
		seq := make([]preTokenizer, 0, len(v.PreTokenizers))
		for _, item := range v.PreTokenizers {
			p, err := buildPreTokenizer(item)
			if err != nil {
				return nil, err
			}
			if p != nil {
				seq = append(seq, p)
			}
		}
		return seqPreTokenizer(seq), nil

	case "Split":
		var v struct {
			Pattern  patternSpec `json:"pattern"`
			Behavior string      `json:"behavior"`
			Invert   bool        `json:"invert"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Split pre_tokenizer 失败: %w", err)
		}
		return newSplitPreTokenizer(v.Pattern, v.Behavior, v.Invert)

	case "ByteLevel":
		// add_prefix_space/use_regex 在 tokenizer.json 中缺省即为 true，
		// 与 HuggingFace 的默认值保持一致。
		v := struct {
			AddPrefixSpace *bool `json:"add_prefix_space"`
			UseRegex       *bool `json:"use_regex"`
		}{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 ByteLevel pre_tokenizer 失败: %w", err)
		}
		bl := &byteLevelPreTokenizer{addPrefixSpace: true, useRegex: true}
		if v.AddPrefixSpace != nil {
			bl.addPrefixSpace = *v.AddPrefixSpace
		}
		if v.UseRegex != nil {
			bl.useRegex = *v.UseRegex
		}
		if bl.useRegex {
			re, err := compilePattern(byteLevelSplitPattern)
			if err != nil {
				return nil, fmt.Errorf("ByteLevel pre_tokenizer: %w", err)
			}
			bl.re = re
		}
		return bl, nil

	case "Whitespace":
		return newSplitPattern(`\w+|[^\w\s]+`, behaviorIsolated, false)

	case "WhitespaceSplit":
		return newSplitPattern(`\s+`, behaviorRemoved, false)

	case "Punctuation":
		var v struct {
			Behavior string `json:"behavior"`
		}
		_ = json.Unmarshal(raw, &v)
		if v.Behavior == "" {
			v.Behavior = behaviorIsolated
		}
		return &funcSplitPreTokenizer{behavior: v.Behavior, match: isPunctuation}, nil

	case "Digits":
		var v struct {
			IndividualDigits bool `json:"individual_digits"`
		}
		_ = json.Unmarshal(raw, &v)
		expr := `\p{N}+`
		if v.IndividualDigits {
			expr = `\p{N}`
		}
		return newSplitPattern(expr, behaviorIsolated, false)

	default:
		// 宁可明确报错触发降级，也不要静默按错误语义切分——那会产出看似
		// 合理、实则不可信的 token 数。
		return nil, fmt.Errorf("暂不支持的 pre_tokenizer 类型 %q", head.Type)
	}
}

// Split 的分隔符归属策略。
const (
	behaviorRemoved            = "Removed"
	behaviorIsolated           = "Isolated"
	behaviorMergedWithPrevious = "MergedWithPrevious"
	behaviorMergedWithNext     = "MergedWithNext"
	behaviorContiguous         = "Contiguous"
)

// byteLevelSplitPattern 是 GPT-2 以来 ByteLevel 内置的切分正则。
const byteLevelSplitPattern = `'s|'t|'re|'ve|'m|'ll|'d| ?\p{L}+| ?\p{N}+| ?[^\s\p{L}\p{N}]+|\s+(?!\S)|\s+`

type seqPreTokenizer []preTokenizer

func (s seqPreTokenizer) apply(pieces []string) ([]string, error) {
	var err error
	for _, p := range s {
		if pieces, err = p.apply(pieces); err != nil {
			return nil, err
		}
	}
	return pieces, nil
}

// splitPreTokenizer 按正则切分，分隔符归属由 behavior 决定。
type splitPreTokenizer struct {
	re       *pattern
	behavior string
	invert   bool
}

func newSplitPreTokenizer(p patternSpec, behavior string, invert bool) (preTokenizer, error) {
	switch {
	case p.Regex != nil:
		return newSplitPattern(*p.Regex, behavior, invert)
	case p.String != nil:
		return &literalSplitPreTokenizer{sep: *p.String, behavior: behavior, invert: invert}, nil
	default:
		return nil, fmt.Errorf("Split pre_tokenizer 缺少 pattern")
	}
}

func newSplitPattern(expr, behavior string, invert bool) (preTokenizer, error) {
	re, err := compilePattern(expr)
	if err != nil {
		return nil, fmt.Errorf("Split pre_tokenizer: %w", err)
	}
	if behavior == "" {
		behavior = behaviorIsolated
	}
	return &splitPreTokenizer{re: re, behavior: behavior, invert: invert}, nil
}

func (p *splitPreTokenizer) apply(pieces []string) ([]string, error) {
	out := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		out = append(out, assemble(segmentize(piece, p.re.findAll(piece), p.invert), p.behavior)...)
	}
	return out, nil
}

// literalSplitPreTokenizer 处理 {"String": "..."} 形式的字面量分隔符。
type literalSplitPreTokenizer struct {
	sep      string
	behavior string
	invert   bool
}

func (p *literalSplitPreTokenizer) apply(pieces []string) ([]string, error) {
	if p.sep == "" {
		return pieces, nil
	}
	out := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		var spans []span
		for at := 0; ; {
			i := strings.Index(piece[at:], p.sep)
			if i < 0 {
				break
			}
			spans = append(spans, span{at + i, at + i + len(p.sep)})
			at += i + len(p.sep)
		}
		out = append(out, assemble(segmentize(piece, spans, p.invert), p.behavior)...)
	}
	return out, nil
}

// funcSplitPreTokenizer 按逐字符谓词切分，用于 Punctuation 这类无需正则的规则。
type funcSplitPreTokenizer struct {
	behavior string
	match    func(rune) bool
}

func (p *funcSplitPreTokenizer) apply(pieces []string) ([]string, error) {
	out := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		var spans []span
		for i, r := range piece {
			if p.match(r) {
				spans = append(spans, span{i, i + len(string(r))})
			}
		}
		out = append(out, assemble(segmentize(piece, spans, false), p.behavior)...)
	}
	return out, nil
}

func isPunctuation(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// byteLevelPreTokenizer 完成 ByteLevel 的三件事：可选补前导空格、可选正则切分、
// 字节到 Unicode 的映射。映射之后片段即可直接查 BPE 词表。
type byteLevelPreTokenizer struct {
	addPrefixSpace bool
	useRegex       bool
	re             *pattern
}

func (p *byteLevelPreTokenizer) apply(pieces []string) ([]string, error) {
	out := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		if p.addPrefixSpace && !strings.HasPrefix(piece, " ") {
			piece = " " + piece
		}
		if !p.useRegex {
			out = append(out, byteLevelEncode(piece))
			continue
		}
		for _, sub := range assemble(segmentize(piece, p.re.findAll(piece), false), behaviorIsolated) {
			out = append(out, byteLevelEncode(sub))
		}
	}
	return out, nil
}

// segment 是切分的中间表示：一段文本，以及它是否命中了分隔符。
type segment struct {
	text    string
	isMatch bool
}

// segmentize 把字符串按匹配区间拆成交替的命中/未命中段。
// invert 为真时互换两者身份，对应 tokenizer.json 中的 "invert": true。
func segmentize(s string, spans []span, invert bool) []segment {
	segs := make([]segment, 0, len(spans)*2+1)
	prev := 0
	for _, sp := range spans {
		if sp.start > prev {
			segs = append(segs, segment{s[prev:sp.start], false})
		}
		segs = append(segs, segment{s[sp.start:sp.end], true})
		prev = sp.end
	}
	if prev < len(s) {
		segs = append(segs, segment{s[prev:], false})
	}
	if invert {
		for i := range segs {
			segs[i].isMatch = !segs[i].isMatch
		}
	}
	return segs
}

// assemble 按 behavior 把中间段组装成最终片段。
func assemble(segs []segment, behavior string) []string {
	out := make([]string, 0, len(segs))
	appendNonEmpty := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}

	switch behavior {
	case behaviorRemoved:
		for _, seg := range segs {
			if !seg.isMatch {
				appendNonEmpty(seg.text)
			}
		}

	case behaviorMergedWithPrevious:
		// 分隔符并入前一段；若前面没有内容则自成一段。
		var buf strings.Builder
		for _, seg := range segs {
			buf.WriteString(seg.text)
			if seg.isMatch {
				appendNonEmpty(buf.String())
				buf.Reset()
			}
		}
		appendNonEmpty(buf.String())

	case behaviorMergedWithNext:
		// 分隔符并入后一段；若后面没有内容则自成一段。
		var pending string
		for _, seg := range segs {
			if seg.isMatch {
				// 连续的分隔符累积，等待下一段非分隔内容。
				pending += seg.text
				continue
			}
			appendNonEmpty(pending + seg.text)
			pending = ""
		}
		appendNonEmpty(pending)

	case behaviorContiguous:
		// 相邻且身份相同的段合并成一个片段。
		var buf strings.Builder
		var cur bool
		started := false
		for _, seg := range segs {
			if started && seg.isMatch != cur {
				appendNonEmpty(buf.String())
				buf.Reset()
			}
			buf.WriteString(seg.text)
			cur, started = seg.isMatch, true
		}
		appendNonEmpty(buf.String())

	default: // behaviorIsolated
		for _, seg := range segs {
			appendNonEmpty(seg.text)
		}
	}

	return out
}
