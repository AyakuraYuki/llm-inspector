package tokenizers

import (
	"fmt"
	"regexp"
	"time"

	"github.com/dlclark/regexp2"
)

// pattern 封装一条切分正则。
//
// 优先使用标准库 regexp（RE2）：它是线性时间的，比回溯引擎快一个数量级。
// 但 RE2 不支持先行/后行断言，而 \s+(?!\S) 这类写法几乎出现在所有现代 LLM
// 的切分正则里，因此在标准库编译失败时回退到 regexp2。
//
// 这里依赖一个前提：Go 标准库对不支持的语法一律报编译错误，而非静默按别的
// 语义解析，所以“编译通过”即可认为语义与回溯引擎一致。两条路径的实际一致性
// 由 golden 对拍测试守住。
type pattern struct {
	std *regexp.Regexp
	alt *regexp2.Regexp
}

// regexpTimeout 给回溯引擎兜底。切分正则作用在模型输出上，长度不可控，
// 病态回溯会让整个评测卡死，超时后按“未匹配”处理远好于挂起。
const regexpTimeout = 5 * time.Second

func compilePattern(expr string) (*pattern, error) {
	if re, err := regexp.Compile(expr); err == nil {
		return &pattern{std: re}, nil
	}
	re, err := regexp2.Compile(expr, regexp2.None)
	if err != nil {
		return nil, fmt.Errorf("编译正则 %q 失败: %w", expr, err)
	}
	re.MatchTimeout = regexpTimeout
	return &pattern{alt: re}, nil
}

// span 是一个左闭右开的字节区间。
type span struct{ start, end int }

// findAll 返回所有非重叠匹配的字节区间，按出现顺序排列。
// 无匹配时返回 nil，两条引擎路径的空结果表示保持一致。
func (p *pattern) findAll(s string) []span {
	if p.std != nil {
		idx := p.std.FindAllStringIndex(s, -1)
		var out []span
		for _, m := range idx {
			// 跳过空匹配：它不消耗输入，对切分没有意义，
			// 且会在 Isolated 语义下产生大量空片段。
			if m[1] > m[0] {
				out = append(out, span{m[0], m[1]})
			}
		}
		return out
	}

	// regexp2 的偏移以 UTF-16 code unit 计，需转换回字节偏移。
	runes := []rune(s)
	offsets := runeOffsets(s, len(runes))

	var out []span
	m, err := p.alt.FindRunesMatch(runes)
	for err == nil && m != nil {
		if m.Length > 0 {
			out = append(out, span{offsets[m.Index], offsets[m.Index+m.Length]})
		}
		m, err = p.alt.FindNextMatch(m)
	}
	return out
}

// runeOffsets 返回长度为 n+1 的表，第 i 项是第 i 个 rune 的起始字节偏移。
func runeOffsets(s string, n int) []int {
	offsets := make([]int, 0, n+1)
	for i := range s {
		offsets = append(offsets, i)
	}
	return append(offsets, len(s))
}
