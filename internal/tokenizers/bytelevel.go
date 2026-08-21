package tokenizers

// ByteLevel 编码把任意字节映射到一个可打印的 Unicode 码点，使得 BPE 可以在
// 纯文本词表上工作而不会遇到控制字符或非法 UTF-8 序列。映射表沿用 GPT-2 的
// bytes_to_unicode：可打印 ASCII 与 Latin-1 段保持原样，其余 68 个字节顺次
// 映射到 U+0100 起的私用区间。词表里常见的 "Ġ" 就是空格（0x20）的映射结果。

var (
	byteToRune [256]rune
	runeToByte map[rune]byte
)

func init() {
	// 保持原样的字节区间，与 GPT-2 bytes_to_unicode 完全一致。
	keep := make([]bool, 256)
	for b := '!'; b <= '~'; b++ {
		keep[b] = true
	}
	for b := 0xa1; b <= 0xac; b++ {
		keep[b] = true
	}
	for b := 0xae; b <= 0xff; b++ {
		keep[b] = true
	}

	runeToByte = make(map[rune]byte, 256)
	next := rune(256)
	for b := range 256 {
		var r rune
		if keep[b] {
			r = rune(b)
		} else {
			r = next
			next++
		}
		byteToRune[b] = r
		runeToByte[r] = byte(b)
	}
}

// byteLevelEncode 把片段的原始字节逐个映射为 Unicode 字符。
func byteLevelEncode(s string) string {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); i++ {
		out = append(out, byteToRune[s[i]])
	}
	return string(out)
}
