package sse

// StreamSummary 是一次 SSE 流消费后的事实摘要（协议无关）。
// 分类决策（如何把事实映射为"成功/截断/上游错误"）由调用方完成；
// 本类型只记录流中观察到的事实。时间单位均为毫秒（float64），
// 由调用方注入（共享层不触碰 time.Now()，保证可测试性）。
type StreamSummary struct {
	TTFTMS            float64 // 首个有内容（含思考）事件到达时刻；<0 表示未捕获到输出内容
	FirstByteMS       float64 // 首字节到达时刻（HTTP 层注入），TTFT 回退用；0 表示未注入
	PromptTokens      int64
	CompletionTokens  int64
	CachedInputTokens int64
	UsageSeen         bool     // 出现过携带 completion token 的 usage 事件
	TerminalSeen      bool     // 出现过协议终止标记（[DONE]/finish_reason/message_stop 等）
	UpstreamErr       string   // 流内错误事件的首个错误消息
	TextParts         []string // 收集的文本片段（usage 缺失时的 token 估算回退）
}

// NewStreamSummary 创建初始化的摘要。TTFTMS 初始为 -1（"未捕获到输出内容"），
// 与 evaluation 的 Result.TTFTMS 惯例一致。
func NewStreamSummary() *StreamSummary {
	return &StreamSummary{TTFTMS: -1}
}

// ApplySSEEvent 将单个已解析的 SSE 数据事件合并进摘要。
// nowMS 为事件到达时刻（距请求发起点的毫秒数，由调用方注入）。
// 处理顺序与原 parseStreamMetrics 的扫描循环一致：
// 错误 → 终止标记 → 输出内容（TTFT）→ usage。
func ApplySSEEvent(obj map[string]any, nowMS float64, s *StreamSummary) {
	if msg, found := ErrorInfo(obj); found && s.UpstreamErr == "" {
		s.UpstreamErr = msg
	}
	if IsTerminal(obj) {
		s.TerminalSeen = true
	}
	if s.TTFTMS < 0 && HasOutputContent(obj) {
		s.TTFTMS = nowMS
	}
	p, c, ct, _ := ConsumeUsage(obj, &s.TextParts)
	if c >= 0 {
		s.CompletionTokens = c
		s.UsageSeen = true
	}
	if p > 0 {
		s.PromptTokens = p
	}
	if ct >= 0 {
		s.CachedInputTokens = ct
	}
}
