// Package tokstats 提供各 cmd 工具共用的 token 统计原语：
// 无 usage 上报时的统一 token 估算、per-request 生成速率样本的
// 有效性判定（生成窗口双门槛 + 单流物理天花板）。
package tokstats

import (
	"time"
	"unicode/utf8"
)

// per-request 生成速率样本的有效性门槛。整条响应被缓冲后一次性到达时
// （网关伪流式转发、压测机读流饥饿），TTFT 会贴着流结束打点，gen_window
// 只剩缓冲区排空耗时（µs~ms 级），output_tokens/gen_window 测出的是排空
// 速度而非模型解码速度，能虚高到千万 tok/s 量级，直接打爆 TPS/TPM 高分位。
// 这类样本的特征是 gen_window 绝对值极小、或占 E2E 的比例趋近于零。
const (
	MinGenWindow     = 2 * time.Millisecond // gen_window 绝对下限
	MinGenWindowFrac = 0.05                 // gen_window 占 E2E 的最小比例（TTFT ≥ 95% E2E 即视为一次性到达）
)

// MaxPlausibleStreamTPS 是单流解码速度的物理天花板（tok/s）。
// 当代最快的推理硬件单流解码约 2,000~3,000 tok/s，超出该值的样本
// 不可能是真实解码速度，必为测量假象（缓冲区排空、usage 虚报等）。
// 双门槛管「窗口萎缩」这一种形态，天花板兜住任何漏网形态。
// 声明为变量以便测试按需调整。
var MaxPlausibleStreamTPS = 4096.0

// ValidGenWindow 判定生成窗口是否足以测量真实解码速度：
// 需同时满足绝对下限与占 E2E 比例下限。
func ValidGenWindow(genWindow, e2e time.Duration) bool {
	return genWindow >= MinGenWindow &&
		float64(genWindow) >= float64(e2e)*MinGenWindowFrac
}

// ValidStreamTPS 判定一个 per-request TPS 样本（outputTokens 在 genWindow
// 内解码得出）是否通过全部有效性校验：窗口双门槛 + 物理天花板。
func ValidStreamTPS(outputTokens int64, genWindow, e2e time.Duration) bool {
	if outputTokens <= 0 || genWindow <= 0 {
		return false
	}
	if !ValidGenWindow(genWindow, e2e) {
		return false
	}
	return float64(outputTokens)/genWindow.Seconds() <= MaxPlausibleStreamTPS
}

// EstimateTokens 在 provider 未上报 usage 时按文本构成估算 token 数。
// 经验比率：ASCII 文本约 4 字符/token，CJK 等多字节字符约 1.5 字符/token；
// 混合文本按字符构成加权——比按字节数一刀切（英文口径）在中文输出下
// 低估约一半的旧做法更接近真实值。估算结果仅用于速率指标兜底，
// 不应作为计费/对账依据。
func EstimateTokens(text string) int64 {
	if text == "" {
		return 0
	}
	var ascii, other int64
	for i := 0; i < len(text); {
		if text[i] < utf8.RuneSelf {
			ascii++
			i++
		} else {
			_, size := utf8.DecodeRuneInString(text[i:])
			other++
			i += size
		}
	}
	n := max(ascii/4+int64(float64(other)/1.5), 1)
	return n
}
