package tokenizers

import (
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// normalizer 在预切分之前对文本做规范化。
type normalizer interface {
	normalize(s string) string
}

func buildNormalizer(raw json.RawMessage) (normalizer, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("解析 normalizer 失败: %w", err)
	}

	switch head.Type {
	case "Sequence":
		var v struct {
			Normalizers []json.RawMessage `json:"normalizers"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Sequence normalizer 失败: %w", err)
		}
		seq := make([]normalizer, 0, len(v.Normalizers))
		for _, item := range v.Normalizers {
			n, err := buildNormalizer(item)
			if err != nil {
				return nil, err
			}
			if n != nil {
				seq = append(seq, n)
			}
		}
		if len(seq) == 0 {
			return nil, nil
		}
		return seqNormalizer(seq), nil

	case "NFC":
		return formNormalizer{norm.NFC}, nil
	case "NFD":
		return formNormalizer{norm.NFD}, nil
	case "NFKC":
		return formNormalizer{norm.NFKC}, nil
	case "NFKD":
		return formNormalizer{norm.NFKD}, nil

	case "Lowercase":
		return lowercaseNormalizer{}, nil

	case "Strip":
		var v struct {
			StripLeft  bool `json:"strip_left"`
			StripRight bool `json:"strip_right"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Strip normalizer 失败: %w", err)
		}
		return stripNormalizer{left: v.StripLeft, right: v.StripRight}, nil

	case "Prepend":
		var v struct {
			Prepend string `json:"prepend"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Prepend normalizer 失败: %w", err)
		}
		return prependNormalizer{prefix: v.Prepend}, nil

	case "Replace":
		var v struct {
			Pattern patternSpec `json:"pattern"`
			Content string      `json:"content"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("解析 Replace normalizer 失败: %w", err)
		}
		return newReplaceNormalizer(v.Pattern, v.Content)

	default:
		return nil, fmt.Errorf("暂不支持的 normalizer 类型 %q", head.Type)
	}
}

type seqNormalizer []normalizer

func (s seqNormalizer) normalize(text string) string {
	for _, n := range s {
		text = n.normalize(text)
	}
	return text
}

type formNormalizer struct{ f norm.Form }

func (n formNormalizer) normalize(s string) string { return n.f.String(s) }

type lowercaseNormalizer struct{}

func (lowercaseNormalizer) normalize(s string) string { return strings.ToLower(s) }

type stripNormalizer struct{ left, right bool }

func (n stripNormalizer) normalize(s string) string {
	if n.left {
		s = strings.TrimLeft(s, " \t\n\r\v\f")
	}
	if n.right {
		s = strings.TrimRight(s, " \t\n\r\v\f")
	}
	return s
}

type prependNormalizer struct{ prefix string }

func (n prependNormalizer) normalize(s string) string { return n.prefix + s }

// replaceNormalizer 覆盖 Replace 的两种 pattern 形式：字面量串与正则。
type replaceNormalizer struct {
	literal string
	re      *pattern
	content string
}

func newReplaceNormalizer(p patternSpec, content string) (normalizer, error) {
	if p.String != nil {
		return &replaceNormalizer{literal: *p.String, content: content}, nil
	}
	if p.Regex != nil {
		re, err := compilePattern(*p.Regex)
		if err != nil {
			return nil, fmt.Errorf("Replace normalizer: %w", err)
		}
		return &replaceNormalizer{re: re, content: content}, nil
	}
	return nil, fmt.Errorf("Replace normalizer 缺少 pattern")
}

func (n *replaceNormalizer) normalize(s string) string {
	if n.re == nil {
		return strings.ReplaceAll(s, n.literal, n.content)
	}
	spans := n.re.findAll(s)
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		b.WriteString(s[prev:sp.start])
		b.WriteString(n.content)
		prev = sp.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// patternSpec 对应 tokenizer.json 中的 {"String": "..."} 或 {"Regex": "..."}。
type patternSpec struct {
	String *string `json:"String"`
	Regex  *string `json:"Regex"`
}
