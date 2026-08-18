package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/util"
)

// Judge 使用一个更强的模型对开放式回答打分（LLM-as-a-Judge）。
type Judge struct {
	p provider.Provider
}

// NewJudge 以给定端点创建裁判。judge 配置为空时应返回 nil。
func NewJudge(p provider.Provider) *Judge {
	if p == nil {
		return nil
	}
	return &Judge{p: p}
}

const judgePrompt = `你是一个严格的评测裁判。请根据评分标准，对"模型回答"进行打分。

【评分标准】
%s

【模型输入】
%s

【模型回答】
%s

请只输出 JSON，不要输出其他内容：{"score": <0-10 的整数>, "reason": "<不超过 50 字的理由>"}`

type judgeResponse struct {
	Reason string  `json:"reason"`
	Score  float64 `json:"score"`
}

// Score 让裁判模型按 rubric 给 answer 打分，返回 0..1 的归一化分数。
// question 为模型输入（通常是最后一个 user 消息或全部轮次的拼接）。
func (j *Judge) Score(ctx context.Context, question, answer, rubric string) (Verdict, error) {
	if j == nil || j.p == nil {
		return Verdict{}, fmt.Errorf("未配置裁判模型")
	}
	if rubric == "" {
		rubric = "回答正确、切题、表达清晰"
	}
	zero := 0.0
	prompt := fmt.Sprintf(judgePrompt, rubric, question, answer)
	resp, err := j.p.Chat(ctx, &provider.Request{
		Messages:    []provider.Message{{Role: "user", Content: prompt}},
		MaxTokens:   1024,
		Temperature: &zero,
		JSONMode:    true,
	})
	if err != nil {
		// 部分服务不支持 JSON mode，降级为普通请求再试一次
		if provider.StatusCode(err) == 400 {
			resp, err = j.p.Chat(ctx, &provider.Request{
				Messages:    []provider.Message{{Role: "user", Content: prompt}},
				MaxTokens:   1024,
				Temperature: &zero,
			})
		}
		if err != nil {
			return Verdict{}, fmt.Errorf("裁判模型调用失败: %w", err)
		}
	}
	var jr judgeResponse
	if err = json.Unmarshal([]byte(stripCodeFence(strings.TrimSpace(resp.Content))), &jr); err != nil {
		return Verdict{}, fmt.Errorf("裁判输出解析失败: %q", util.TruncateString(resp.Content, 120))
	}
	score := jr.Score / 10
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return Verdict{Score: score, Reason: jr.Reason}, nil
}
