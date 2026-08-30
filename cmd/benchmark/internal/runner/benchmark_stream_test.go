package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/benchmark/internal/types"
	"github.com/AyakuraYuki/llm-inspector/pkg/go-openai"
)

// sseTestServer 按给定节奏吐出 SSE chunk 的测试服务器。
// 每个元素是一个 data 载荷；delay 为 chunk 间的间隔（用于控制 TTFT/生成窗口）。
func sseTestServer(t *testing.T, chunks []string, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer does not support flush")
			return
		}
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(srv *httptest.Server) *openai.Client {
	conf := openai.DefaultConfig("test-key")
	conf.BaseURL = srv.URL
	return openai.NewClientWithConfig(conf)
}

// 思考内容应先于正文到达：TTFT 必须在第一个 reasoning_content 处打点，
// 否则生成窗口萎缩成正文段，解码 TPS 被虚高。
func TestBenchmarkQuestion_ReasoningTriggersTTFT(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"reasoning_content":"let me think "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"step by step "}}]}`,
		`{"choices":[{"delta":{"content":"The answer is "}}]}`,
		`{"choices":[{"delta":{"content":"\\boxed{42}"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`,
	}
	// 第一个 chunk 立刻到达，后续每个间隔 100ms：
	// 若 TTFT 在思考处打点，TTFT ≈ 0；若等到正文才打点，TTFT ≥ 200ms
	srv := sseTestServer(t, chunks, 100*time.Millisecond)

	result := benchmarkQuestion(newTestClient(srv), "test-model",
		types.Question{Dataset: "test", Question: "q"}, 0, config.BenchmarkConfig{})

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.TTFT >= 200*time.Millisecond {
		t.Errorf("TTFT = %v, want < 200ms（应在首个 reasoning_content 处打点，而不是等到正文）", result.TTFT)
	}
	if result.TokensUsed != 10 {
		t.Errorf("TokensUsed = %d, want 10（usage 上报值）", result.TokensUsed)
	}
	if result.TokensEstimated {
		t.Error("TokensEstimated = true, want false（有 usage 上报）")
	}
	if !result.DecodeValid {
		t.Error("DecodeValid = false, want true（生成窗口约 400ms，应通过校验）")
	}
	if result.DecodeValid {
		// 自校验：解码 TPS ≥ E2E TPS（生成窗口 ≤ E2E）
		if result.TPSDecode < result.TPSE2E {
			t.Errorf("TPSDecode (%f) < TPSE2E (%f)，违反窗口包含关系", result.TPSDecode, result.TPSE2E)
		}
		if result.TPMDecode != result.TPSDecode*60 {
			t.Errorf("TPMDecode = %f, want TPSDecode*60", result.TPMDecode)
		}
	}
}

// 伪流式（整条响应一次性到达）：生成窗口萎缩成缓冲区排空耗时，
// 解码 TPS 必须判伪剔除；E2E TPS 不受影响。
func TestBenchmarkQuestion_BurstExcluded(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"a b c d e f g h "}}]}`,
		`{"choices":[{"delta":{"content":"i j k l m n o p "}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":16,"total_tokens":21}}`,
	}
	srv := sseTestServer(t, chunks, 0) // 无间隔，全部一次性到达

	result := benchmarkQuestion(newTestClient(srv), "test-model",
		types.Question{Dataset: "test", Question: "q"}, 0, config.BenchmarkConfig{})

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.DecodeValid {
		t.Errorf("DecodeValid = true (TPSDecode=%.2f), want false（排空样本必须判伪）", result.TPSDecode)
	}
	if result.TPSE2E <= 0 {
		t.Error("TPSE2E <= 0, want > 0（E2E 口径不受剔除影响）")
	}
}

// 网关未上报 usage：回退到文本估算（正文 + 思考内容），并打估算标记。
func TestBenchmarkQuestion_EstimatedTokensFallback(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"reasoning_content":"思考"}}]}`, // 2 字
		`{"choices":[{"delta":{"content":"你好世界"}}]}`,         // 4 字
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
	}
	srv := sseTestServer(t, chunks, 100*time.Millisecond)

	result := benchmarkQuestion(newTestClient(srv), "test-model",
		types.Question{Dataset: "test", Question: "q"}, 0, config.BenchmarkConfig{})

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// 正文 + 思考共 6 个 CJK 字符，按 1.5 字符/token 估算 = 4
	if result.TokensUsed != 4 {
		t.Errorf("TokensUsed = %d, want 4（文本估算：6 CJK / 1.5）", result.TokensUsed)
	}
	if !result.TokensEstimated {
		t.Error("TokensEstimated = false, want true（无 usage 上报）")
	}
	if !result.DecodeValid {
		t.Error("DecodeValid = false, want true（生成窗口约 100ms，应通过校验）")
	}
}
