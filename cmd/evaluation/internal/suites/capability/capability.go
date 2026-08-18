// Package capability 实现 L3：模型能力评测。
// 从 YAML 数据集加载题目，并发执行，逐题确定性打分（可选裁判模型）。
package capability

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/datasets"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/scorer"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
	"github.com/AyakuraYuki/llm-inspector/internal/util"
)

// Case 是数据集中的一道题。
type Case struct {
	ID       string             `yaml:"id"`
	Category string             `yaml:"category"`
	Turns    []provider.Message `yaml:"turns"`
	Scorer   scorer.Spec        `yaml:"scorer"`
}

// LoadCases 加载数据集；path 为空时使用内建冒烟题库。
func LoadCases(path string) ([]Case, error) {
	data := datasets.Smoke
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取数据集失败: %w", err)
		}
	}
	var cases []Case
	if err := yaml.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("解析数据集失败: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("数据集为空")
	}
	for i, c := range cases {
		if c.ID == "" {
			return nil, fmt.Errorf("第 %d 题缺少 id", i+1)
		}
		if len(c.Turns) == 0 {
			return nil, fmt.Errorf("题目 %s 缺少 turns", c.ID)
		}
	}
	return cases, nil
}

// Run 执行 L3。judge 为 nil 时 judge 类型题记 skip。
func Run(ctx context.Context, p provider.Provider, cfg config.CapabilityConfig, judge *scorer.Judge) types.LayerResult {
	start := time.Now()
	layer := types.LayerResult{ID: "L3", Name: "模型能力", Enabled: true}

	cases, err := LoadCases(cfg.Dataset)
	if err != nil {
		layer.Checks = append(layer.Checks, types.CheckResult{
			Name: "dataset_load", Status: types.StatusFail, Score: 0, Weight: 1, Detail: err.Error(),
		})
		return layer
	}

	fmt.Printf("  加载 %d 道题目，并发度 %d\n", len(cases), cfg.Concurrency)

	checks := make([]types.CheckResult, len(cases))
	var completed int32 // 原子计数器
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := max(cfg.Concurrency, 1)
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				checks[i] = runCase(ctx, p, judge, &cases[i])
				// 原子递增并输出进度
				done := atomic.AddInt32(&completed, 1)
				fmt.Printf("  [%d/%d] %s: %.0f%%\n", done, len(cases), cases[i].ID, checks[i].Score*100)
			}
		})
	}
	for i := range cases {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	layer.Checks = append(layer.Checks, checks...)
	layer.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	return layer
}

// runCase 执行单题并打分。使用命名返回值，defer 才能在 return 之后写入耗时。
func runCase(ctx context.Context, p provider.Provider, judge *scorer.Judge, c *Case) (result types.CheckResult) {
	start := time.Now()
	result = types.CheckResult{Name: c.ID, Weight: 1}
	defer func() {
		result.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	}()

	metrics := map[string]any{"category": c.Category, "scorer": c.Scorer.Type}

	if c.Scorer.Type == "judge" && judge == nil {
		result.Status = types.StatusSkip
		result.Detail = "未配置裁判模型，跳过 judge 打分题"
		result.Metrics = metrics
		return result
	}

	resp, err := p.Chat(ctx, &provider.Request{
		Messages:  c.Turns,
		MaxTokens: 1024,
	})
	if err != nil {
		result.Status = types.StatusFail
		result.Score = 0
		result.Detail = "调用失败: " + err.Error()
		result.Metrics = metrics
		return result
	}
	metrics["output"] = util.TruncateString(resp.Content, 200)
	metrics["completion_tokens"] = resp.CompletionTokens

	var verdict scorer.Verdict
	if c.Scorer.Type == "judge" {
		question := lastUserMessage(c.Turns)
		verdict, err = judge.Score(ctx, question, resp.Content, c.Scorer.Rubric)
	} else {
		verdict, err = scorer.Score(&c.Scorer, resp.Content)
	}
	if err != nil {
		result.Status = types.StatusFail
		result.Score = 0
		result.Detail = "打分失败: " + err.Error()
		result.Metrics = metrics
		return result
	}

	metrics["reason"] = verdict.Reason
	result.Metrics = metrics
	result.Score = verdict.Score
	result.Detail = verdict.Reason
	if verdict.Score >= 0.99 {
		result.Status = types.StatusPass
	} else if verdict.Score > 0 {
		// 部分得分仍记 fail，但保留分值计入层均分
		result.Status = types.StatusFail
	} else {
		result.Status = types.StatusFail
	}
	return result
}

func lastUserMessage(turns []provider.Message) string {
	for i := len(turns) - 1; i >= 0; i-- {
		if strings.EqualFold(turns[i].Role, "user") {
			return turns[i].Content
		}
	}
	return ""
}
