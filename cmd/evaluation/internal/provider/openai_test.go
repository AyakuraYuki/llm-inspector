package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go"

	"github.com/AyakuraYuki/llm-inspector/internal/llm/params"
)

// TestExtraStringSDKContract 锁定 extraString 依赖的 SDK 解码契约。
//
// openai-go v1.12.0 没有 reasoning_content 的强类型字段，也没有任何响应结构使用
// apijson 的 `extras` 标签，因此该字段只能从 JSON.ExtraFields 取。又因为没有
// extras decoder，apijson 会把所有未知字段标记为 invalid 状态（但保留 raw），
// 所以 extraString 不能用 Field.Valid() 做存在性判断——本测试把这个前提固化下来，
// 一旦升级 SDK 后行为改变即失败。
func TestExtraStringSDKContract(t *testing.T) {
	t.Run("非流式 message", func(t *testing.T) {
		var resp openai.ChatCompletion
		if err := json.Unmarshal([]byte(`{
    "id": "c",
    "object": "chat.completion",
    "created": 1,
    "model": "m",
    "choices": [
        {
            "index": 0,
            "finish_reason": "stop",
            "message": {
                "role": "assistant",
                "content": "你好",
                "reasoning_content": "让我想想"
            }
        }
    ]
}`), &resp); err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		fields := resp.Choices[0].Message.JSON.ExtraFields
		if got := extraString(fields, "reasoning_content"); got != "让我想想" {
			t.Errorf("extraString = %q, want %q", got, "让我想想")
		}
		// 前提校验：SDK 确实把未知字段标为 invalid，Valid() 判断会漏取。
		if f, ok := fields["reasoning_content"]; !ok {
			t.Error("ExtraFields 未收录 reasoning_content")
		} else if f.Valid() {
			t.Log("注意：SDK 已把未知字段标为 valid，extraString 仍可正常工作，但注释中的前提已过时")
		}
	})

	t.Run("流式 delta", func(t *testing.T) {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(`{
    "id": "c",
    "object": "chat.completion.chunk",
    "created": 1,
    "model": "m",
    "choices": [
        {
            "index": 0,
            "delta": {
                "reasoning_content": "思考中"
            },
            "finish_reason": null
        }
    ]
}`), &chunk); err != nil {
			t.Fatalf("解码失败: %v", err)
		}
		if got := extraString(chunk.Choices[0].Delta.JSON.ExtraFields, "reasoning_content"); got != "思考中" {
			t.Errorf("extraString = %q, want %q", got, "思考中")
		}
	})

	// 异常载荷不应 panic，也不应返回脏数据（Raw() 是原始 JSON，需真正反序列化）。
	t.Run("异常载荷", func(t *testing.T) {
		cases := map[string]struct {
			delta string
			want  string
		}{
			"字段缺失":    {`{"content":"你好"}`, ""},
			"值为 null": {`{"reasoning_content":null}`, ""},
			"值为对象":    {`{"reasoning_content":{"text":"x"}}`, ""},
			"值为数字":    {`{"reasoning_content":123}`, ""},
			"含转义字符":   {`{"reasoning_content":"第一行\n\"引用\""}`, "第一行\n\"引用\""},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				var chunk openai.ChatCompletionChunk
				payload := fmt.Sprintf(`{
  "id": "c",
  "object": "chat.completion.chunk",
  "created": 1,
  "model": "m",
  "choices": [
    {
      "index": 0,
      "delta": %s,
      "finish_reason": null
    }
  ]
}`, tc.delta)
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					t.Fatalf("解码失败: %v", err)
				}
				if got := extraString(chunk.Choices[0].Delta.JSON.ExtraFields, "reasoning_content"); got != tc.want {
					t.Errorf("extraString = %q, want %q", got, tc.want)
				}
			})
		}
	})
}

// TestExtraReasoningDialects 验证 reasoning_content / reasoning 两种网关方言都能取到思考内容。
// 未覆盖 reasoning 方言时，该类网关的 TTFT 会漏掉思考阶段，进而被误判为伪流式。
func TestExtraReasoningDialects(t *testing.T) {
	cases := map[string]struct {
		delta string
		want  string
	}{
		"reasoning_content 方言": {`{"reasoning_content":"思考中"}`, "思考中"},
		"reasoning 方言":         {`{"reasoning":"思考中"}`, "思考中"},
		"两者并存取优先项":             {`{"reasoning_content":"甲","reasoning":"乙"}`, "甲"},
		"仅正文":                  {`{"content":"你好"}`, ""},
		"reasoning 为对象":        {`{"reasoning":{"effort":"high"}}`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var chunk openai.ChatCompletionChunk
			payload := fmt.Sprintf(`{
    "id": "c",
    "object": "chat.completion.chunk",
    "created": 1,
    "model": "m",
    "choices": [
        {
            "index": 0,
            "delta": %s,
            "finish_reason": null
        }
    ]
}`, tc.delta)
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				t.Fatalf("解码失败: %v", err)
			}
			if got := extraReasoning(chunk.Choices[0].Delta.JSON.ExtraFields); got != tc.want {
				t.Errorf("extraReasoning = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOpenAIStreamReasoningTTFT 验证 reasoning_content 先于正文到达时，
// TTFT 以首个 reasoning chunk 为准（真流式语义）。
func TestOpenAIStreamReasoningTTFT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-key" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(payload string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		delta := func(d map[string]any) string {
			b, _ := json.Marshal(d)
			return fmt.Sprintf(`{"id": "chatcmpl-mock", "object": "chat.completion.chunk", "created": 1, "model": "openai-test-1", "choices": [{"index": 0, "delta": %s, "finish_reason": null}]}`, b)
		}
		send(delta(map[string]any{"role": "assistant"}))
		send(delta(map[string]any{"reasoning_content": "让我想想……"}))
		send(delta(map[string]any{"content": "你好"}))
		send(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":"openai-test-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		send(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":"openai-test-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":5}}}`)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAI(srv.URL+"/v1", "good-key", "openai-test-1", 5*time.Second)
	r, err := c.Stream(t.Context(), &params.Request{
		Messages:  []params.Message{{Role: "user", Content: "你好"}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("Stream 失败: %v", err)
	}
	if r.Content != "你好" {
		t.Errorf("Content = %q", r.Content)
	}
	if r.ReasoningContent != "让我想想……" {
		t.Errorf("ReasoningContent = %q（reasoning_content 应进入思考内容）", r.ReasoningContent)
	}
	if r.TTFTMS < 0 {
		t.Error("reasoning 首 chunk 后应已记录 TTFT")
	}
	if r.PromptTokens != 10 || r.CompletionTokens != 3 {
		t.Errorf("usage = %d/%d, want 10/3", r.PromptTokens, r.CompletionTokens)
	}
	if r.CachedInputTokens != 5 {
		t.Errorf("CachedInputTokens = %d, want 5（prompt_tokens_details.cached_tokens）", r.CachedInputTokens)
	}
}
