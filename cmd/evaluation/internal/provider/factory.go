package provider

import (
	"fmt"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
)

// New 按 target.protocol 构造对应协议的客户端（缺省 openai）。
func New(t config.TargetConfig) (Provider, error) {
	timeout, err := t.TimeoutDuration()
	if err != nil {
		return nil, err
	}
	switch t.ProtocolNormalized() {
	case "openai":
		return NewOpenAI(t.BaseURL, t.APIKey, t.Model, timeout), nil
	case "anthropic":
		return NewAnthropic(t.BaseURL, t.APIKey, t.Model, timeout), nil
	case "gemini":
		return NewGemini(t.BaseURL, t.APIKey, t.Model, timeout), nil
	default:
		return nil, fmt.Errorf("未知协议 %q", t.Protocol)
	}
}
