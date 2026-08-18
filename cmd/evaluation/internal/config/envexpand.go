package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	nullTag      = "!!null"
	boolTag      = "!!bool"
	strTag       = "!!str"
	intTag       = "!!int"
	floatTag     = "!!float"
	timestampTag = "!!timestamp"
	seqTag       = "!!seq"
	mapTag       = "!!map"
	binaryTag    = "!!binary"
	mergeTag     = "!!merge"
)

// 配置文件支持在标量值中插值环境变量：
//
//	${VAR}          必需，未设置则报错
//	${VAR:-def}     未设置或为空时取 def
//	${VAR-def}      仅未设置时取 def（空字符串保留）
//	${VAR:?msg}     必需，未设置或为空时以 msg 报错
//	$$              字面量 $
//
// 只识别 ${...} 形式，裸 $VAR 原样保留（YAML 中 $ 常见于正则与模板片段）。
// 默认值内可继续嵌套 ${...}；环境变量的值本身不再展开，避免注入。
// 展开在 yaml.Node 层进行：注释不参与，且结果只写回标量节点，
// 因此环境变量的值无论包含什么都不会破坏 YAML 结构。
const maxExpandDepth = 10

// expandEnvInNode 就地展开节点树中所有标量的环境变量引用。
// 错误会全部聚合后一次返回，每条带上配置文件中的行列位置。
func expandEnvInNode(root *yaml.Node, path string) error {
	var errs error
	walkScalars(root, func(n *yaml.Node) {
		if !strings.Contains(n.Value, "$") {
			return
		}
		val, err := expandString(n.Value, 0)
		if err != nil {
			// 只回显变量名与位置，不回显展开结果，避免密钥进日志
			for _, e := range flattenJoined(err) {
				errs = errors.Join(errs, fmt.Errorf("%s:%d:%d: %w", path, n.Line, n.Column, e))
			}
			return
		}
		n.Value = val
		// 无引号标量重新推断类型，让 ${RUNS:-20} 仍解析为整数；
		// 带引号的保持字符串，尊重作者的显式意图。
		if n.Style == 0 {
			n.Tag = inferTag(val)
		}
	})
	return errs
}

// isEmptyDocument 判断文档是否没有任何内容。
// 空文件与纯注释文件的 Kind 为 0；`---` 与 `null` 则是包了一个 !!null 标量的文档。
// 这些情况都必须拦在解码之前，否则 defaults() 会写到零值配置上并掩盖问题。
func isEmptyDocument(root *yaml.Node) bool {
	if root == nil || root.IsZero() {
		return true
	}
	if root.Kind == 0 {
		return true
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		return root.Content[0].Tag == nullTag
	}
	return false
}

func walkScalars(n *yaml.Node, fn func(*yaml.Node)) {
	switch n.Kind {
	case yaml.ScalarNode:
		fn(n)
	case yaml.AliasNode: // Value 是锚点名，不参与展开
	default:
		for _, c := range n.Content {
			walkScalars(c, fn)
		}
	}
}

// inferTag 判定展开后标量的 YAML 类型。
// 值一旦会被 YAML 重新解释（引号剥离、多行折叠等）就按字符串处理，
// 保证环境变量的值始终是字面量，不会被当作 YAML 片段。
func inferTag(val string) string {
	var probe yaml.Node
	if err := yaml.Unmarshal([]byte(val), &probe); err != nil {
		return strTag
	}
	if len(probe.Content) != 1 || probe.Content[0].Kind != yaml.ScalarNode {
		return strTag
	}
	if probe.Content[0].Value != val {
		return strTag
	}
	return probe.Content[0].Tag
}

func expandString(s string, depth int) (string, error) {
	if depth > maxExpandDepth {
		return "", fmt.Errorf("变量嵌套展开超过 %d 层", maxExpandDepth)
	}

	var (
		sb   strings.Builder
		errs error
	)
	for i := 0; i < len(s); {
		if s[i] != '$' {
			sb.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' { // $$ 转义
			sb.WriteByte('$')
			i += 2
			continue
		}
		if i+1 >= len(s) || s[i+1] != '{' { // 裸 $ 原样保留
			sb.WriteByte('$')
			i++
			continue
		}
		end, ok := matchBrace(s, i+1)
		if !ok {
			errs = errors.Join(errs, fmt.Errorf("未闭合的变量引用 %q", s[i:]))
			sb.WriteString(s[i:])
			break
		}
		val, err := resolveRef(s[i+2:end], depth)
		if err != nil {
			errs = errors.Join(errs, err)
		}
		sb.WriteString(val)
		i = end + 1
	}
	return sb.String(), errs
}

// matchBrace 返回与 s[open] 处 '{' 配对的 '}' 下标。
// 按深度计数，因此默认值中的 JSON 片段不会被第一个 '}' 截断。
func matchBrace(s string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func resolveRef(ref string, depth int) (string, error) {
	name, rest := splitName(ref)
	if name == "" {
		return "", fmt.Errorf("无效的变量引用 ${%s}", ref)
	}

	val, set := os.LookupEnv(name)
	switch {
	case rest == "":
		if !set {
			return "", fmt.Errorf("环境变量 %s 未设置", name)
		}
		return val, nil

	case strings.HasPrefix(rest, ":-"):
		if set && val != "" {
			return val, nil
		}
		return expandString(rest[2:], depth+1)

	case strings.HasPrefix(rest, "-"):
		if set {
			return val, nil
		}
		return expandString(rest[1:], depth+1)

	case strings.HasPrefix(rest, ":?"):
		if set && val != "" {
			return val, nil
		}
		if msg := strings.TrimSpace(rest[2:]); msg != "" {
			return "", fmt.Errorf("环境变量 %s: %s", name, msg)
		}
		return "", fmt.Errorf("环境变量 %s 未设置或为空", name)

	default:
		return "", fmt.Errorf("无效的变量引用 ${%s}", ref)
	}
}

// splitName 切出引用开头的变量名，余下部分为操作符与参数。
func splitName(ref string) (name, rest string) {
	for i := 0; i < len(ref); i++ {
		if !isNameByte(ref[i], i == 0) {
			return ref[:i], ref[i:]
		}
	}
	return ref, ""
}

func isNameByte(c byte, first bool) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}

// flattenJoined 摊平 errors.Join 聚合的错误，便于逐条加上位置前缀。
func flattenJoined(err error) (out []error) {
	if err == nil {
		return nil
	}

	switch errJoined := err.(type) {
	case interface{ Unwrap() []error }:
		for _, e := range errJoined.Unwrap() {
			out = append(out, flattenJoined(e)...)
		}

	case interface{ Unwrap() error }:
		out = append(out, errJoined.Unwrap())

	default:
		out = []error{err}
	}

	return out
}
