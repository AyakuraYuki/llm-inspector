package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// --- mock OpenAI 兼容服务 ---

type chatReq struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens     int             `json:"max_tokens"`
	Stop          json.RawMessage `json:"stop"`
	Stream        bool            `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	ResponseFormat json.RawMessage `json:"response_format"`
	Tools          json.RawMessage `json:"tools"`
}

// validateParams 模拟标准实现的参数校验：类型错误与越界值返回 400。
// 返回空串表示通过。
func validateParams(raw map[string]any) string {
	numInRange := func(key string, lo, hi float64) string {
		v, ok := raw[key]
		if !ok || v == nil {
			return ""
		}
		f, isNum := v.(float64)
		if !isNum {
			return key + " must be a number"
		}
		if f < lo || f > hi {
			return fmt.Sprintf("%s must be in [%g, %g]", key, lo, hi)
		}
		return ""
	}
	if msg := numInRange("top_p", 0, 1); msg != "" {
		return msg
	}
	if msg := numInRange("temperature", 0, 2); msg != "" {
		return msg
	}
	if msg := numInRange("frequency_penalty", -2, 2); msg != "" {
		return msg
	}
	if msg := numInRange("presence_penalty", -2, 2); msg != "" {
		return msg
	}
	if v, ok := raw["max_tokens"]; ok && v != nil {
		f, isNum := v.(float64)
		if !isNum {
			return "max_tokens must be an integer"
		}
		if f <= 0 || f > 1e8 {
			return "max_tokens out of range"
		}
	}
	msgs, ok := raw["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return "messages must be a non-empty array"
	}
	validRoles := map[string]bool{"system": true, "user": true, "assistant": true, "tool": true}
	for _, m := range msgs {
		obj, isObj := m.(map[string]any)
		if !isObj {
			return "message must be an object"
		}
		role, hasRole := obj["role"].(string)
		if !hasRole || !validRoles[role] {
			return "invalid message role"
		}
		if c, present := obj["content"]; present && c == nil {
			return "message content must not be null"
		}
	}
	return ""
}

// newMockServer 返回一个行为可预测的 OpenAI 兼容 mock 服务。
// 鉴权：仅接受 "Bearer test-key"。上下文上限：约 10 万字符。
func newMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r) {
			writeError(w, 401, "invalid api key")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"mock-model","object":"model","created":1,"owned_by":"mock"}]}`)
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r) {
			writeError(w, 401, "invalid api key")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, 400, "bad request")
			return
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			writeError(w, 400, "bad request")
			return
		}
		if msg := validateParams(raw); msg != "" {
			writeError(w, 400, msg)
			return
		}
		var req chatReq
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, 400, "bad request")
			return
		}
		if req.Model == "nonexistent-model-00000000" {
			writeError(w, 404, "model not found")
			return
		}
		last := lastUser(&req)

		// 上下文上限：约 10 万字符
		if utf8.RuneCountInString(last) > 100000 {
			writeError(w, 400, "context length exceeded")
			return
		}

		// tool calling
		if len(req.Tools) > 0 && strings.Contains(last, "天气") {
			// 工具结果回传：messages 中出现 role=tool 时给出引用结果的最终回答
			for _, m := range req.Messages {
				if m.Role == "tool" {
					writeCompletion(w, &req, "巴黎现在气温 19°C，天气晴。", "stop", "")
					return
				}
			}
			// 并行调用：提供了 get_time 工具且要求同时查询时返回两个调用
			if strings.Contains(string(req.Tools), "get_time") && strings.Contains(last, "同时") {
				writeCompletion(w, &req, "", "tool_calls", `{"role":"assistant","content":"","tool_calls":[`+
					`{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}},`+
					`{"id":"call_2","type":"function","function":{"name":"get_time","arguments":"{\"city\":\"Paris\"}"}}]}`)
				return
			}
			writeCompletion(w, &req, "", "tool_calls", `{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}`)
			return
		}

		// JSON mode / json_schema
		if len(req.ResponseFormat) > 0 {
			if strings.Contains(string(req.ResponseFormat), "json_schema") {
				writeCompletion(w, &req, `{"city":"Paris","country":"France","tags":["capital","art"]}`, "stop", "")
				return
			}
			writeCompletion(w, &req, `{"city":"Paris"}`, "stop", "")
			return
		}

		answer := routeAnswer(&req, last)
		// stop 停止词：在停止词处截断
		if len(req.Stop) > 0 {
			var stops []string
			var single string
			if json.Unmarshal(req.Stop, &stops) != nil && json.Unmarshal(req.Stop, &single) == nil {
				stops = []string{single}
			}
			for _, s := range stops {
				if i := strings.Index(answer, s); i >= 0 {
					answer = answer[:i]
				}
			}
		}
		finish := "stop"
		if req.MaxTokens > 0 && req.MaxTokens <= 16 {
			finish = "length"
			if len(answer) > req.MaxTokens*4 {
				answer = answer[:req.MaxTokens*4]
			}
		}

		if req.Stream {
			writeStream(w, &req, answer, finish)
			return
		}
		writeCompletion(w, &req, answer, finish, "")
	})

	return httptest.NewServer(mux)
}

func authOK(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer test-key"
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":{"message":%q,"type":"mock_error","code":%d}}`, msg, code)
}

func lastUser(req *chatReq) string {
	for _, v := range slices.Backward(req.Messages) {
		if v.Role == "user" {
			return v.Content
		}
	}
	return ""
}

func routeAnswer(req *chatReq, last string) string {
	// system prompt 前缀检查
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "评测前缀") {
			return "评测前缀：你好，很高兴见到你。"
		}
	}
	switch {
	case strings.Contains(last, "一字不差地复述这句话："):
		return strings.TrimSpace(strings.SplitN(last, "：", 2)[1])
	case strings.Contains(last, "一字不差地复述："):
		return strings.TrimSpace(strings.SplitN(last, "：", 2)[1])
	case strings.Contains(last, "几行文字"):
		return "2"
	case strings.Contains(last, "system/系统 指令"):
		return "无"
	case strings.Contains(last, "17*23"):
		return "395"
	case strings.Contains(last, "23*17"):
		return "391"
	case strings.Contains(last, "「hello」"):
		return "olleh"
	case strings.Contains(last, "「world」"):
		return "dlrow"
	case strings.Contains(last, "Red Planet"):
		return "Mars"
	case strings.Contains(last, "长诗"):
		return "秋风起兮白云飞，草木黄落兮雁南归。兰有秀兮菊有芳，怀佳人兮不能忘。"
	case strings.Contains(last, "1 到 1000000 之间的整数"):
		return "42"
	case strings.Contains(last, "从 1 数到 10"):
		return "1 2 3 4 5 6 7 8 9 10"
	case strings.Contains(last, "暗号"):
		return "蓝色狐狸"
	case strings.Contains(last, "15 加 27") || strings.Contains(last, "15 + 27"):
		return "42"
	case strings.Contains(last, "法国的首都"):
		return "巴黎"
	case strings.Contains(last, "回复 ok"):
		return "ok"
	case strings.Contains(last, "机器学习"):
		return "机器学习是人工智能的一个分支。它让计算机从数据中学习规律。它被广泛应用于预测与分类任务。"
	case strings.Contains(last, "无意义文本"):
		return "OK"
	case strings.Contains(last, "ping"):
		return "pong"
	case strings.Contains(last, "你好") || strings.Contains(last, "打个招呼"):
		return "你好"
	default:
		return "OK"
	}
}

func usageJSON(req *chatReq) string {
	return `{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}`
}

// writeCompletion 写非流式响应。messageJSON 非空时直接使用该 message 对象（用于 tool_calls）。
func writeCompletion(w http.ResponseWriter, req *chatReq, content, finish, messageJSON string) {
	w.Header().Set("Content-Type", "application/json")
	msg := messageJSON
	if msg == "" {
		b, _ := json.Marshal(map[string]string{"role": "assistant", "content": content})
		msg = string(b)
	}
	fmt.Fprintf(w, `{"id":"chatcmpl-mock","object":"chat.completion","created":1,"model":%q,`+
		`"choices":[{"index":0,"message":%s,"finish_reason":%q}],"usage":%s}`,
		req.Model, msg, finish, usageJSON(req))
}

func writeStream(w http.ResponseWriter, req *chatReq, content, finish string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)

	send := func(payload string) {
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk := func(delta, finishReason string) string {
		fr := "null"
		if finishReason != "" {
			fr = fmt.Sprintf("%q", finishReason)
		}
		return fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":%q,`+
			`"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`, req.Model, delta, fr)
	}

	send(chunk(`{"role":"assistant"}`, ""))
	half := len(content) / 2
	c1, _ := json.Marshal(map[string]string{"content": content[:half]})
	c2, _ := json.Marshal(map[string]string{"content": content[half:]})
	send(chunk(string(c1), ""))
	send(chunk(string(c2), ""))
	send(chunk(`{}`, finish))
	if req.StreamOptions != nil && req.StreamOptions.IncludeUsage {
		send(fmt.Sprintf(`{"id":"chatcmpl-mock","object":"chat.completion.chunk","created":1,"model":%q,"choices":[],"usage":%s}`,
			req.Model, usageJSON(req)))
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// --- 端到端测试 ---

// writeTestConfig 生成指向 mock 服务的评测配置与数据集，返回配置路径。
func writeTestConfig(t *testing.T, baseURL string) string {
	t.Helper()
	dir := t.TempDir()

	dataset := filepath.Join(dir, "dataset.yaml")
	dsContent := `
- id: e2e-math
  category: reasoning
  turns: [{role: user, content: "计算 17*23+4，只输出数字"}]
  scorer: {type: numeric, expected: 395}
- id: e2e-str
  category: string_ops
  turns: [{role: user, content: "把字符串「hello」反转，只输出结果"}]
  scorer: {type: exact_match, expected: "olleh"}
- id: e2e-know
  category: knowledge
  turns: [{role: user, content: "Which planet is known as the Red Planet? Answer with one word."}]
  scorer: {type: contains, keywords: ["Mars"]}
`
	if err := os.WriteFile(dataset, []byte(dsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(dir, "eval.yaml")
	cfgContent := fmt.Sprintf(`
target:
  base_url: %s/v1
  api_key: test-key
  model: mock-model
  timeout: 10s
layers:
  capability: {dataset: %q, concurrency: 2}
  stability: {samples: 2, soak_requests: 5, temperature: 1.0}
  performance: {runs: 3, concurrency: [1, 2], max_probe_tokens: 32768}
thresholds: {min_layer_score: 0.6}
`, baseURL, dataset)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func findLayer(r *types.Report, id string) *types.LayerResult {
	for i := range r.Layers {
		if r.Layers[i].ID == id {
			return &r.Layers[i]
		}
	}
	return nil
}

func findCheck(l *types.LayerResult, name string) *types.CheckResult {
	for i := range l.Checks {
		if l.Checks[i].Name == name {
			return &l.Checks[i]
		}
	}
	return nil
}

func TestRunPipelineEndToEnd(t *testing.T) {
	srv := newMockServer(t)
	defer srv.Close()

	cfg, err := config.Load(writeTestConfig(t, srv.URL), filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	// 五层都应执行且未被跳过
	for _, id := range []string{"L1", "L2", "L3", "L4", "L5"} {
		l := findLayer(r, id)
		if l == nil {
			t.Fatalf("缺少层 %s", id)
		}
		if l.Skipped || !l.Enabled {
			t.Fatalf("层 %s 不应被跳过", id)
		}
	}

	// L1 全过
	l1 := findLayer(r, "L1")
	if !l1.Passed || l1.Score != 1 {
		t.Fatalf("L1 应满分通过, score=%v passed=%v", l1.Score, l1.Passed)
	}

	// L2 关键检查项
	l2 := findLayer(r, "L2")
	for _, name := range []string{"streaming_sse", "json_mode", "tool_calling", "system_prompt", "multi_turn", "usage_field"} {
		c := findCheck(l2, name)
		if c == nil {
			t.Fatalf("L2 缺少检查项 %s", name)
		}
		if c.Status != types.StatusPass || c.Score < 1 {
			t.Errorf("L2/%s 应满分通过, 实际 status=%s score=%v detail=%s", name, c.Status, c.Score, c.Detail)
		}
	}

	// L3 三题全对
	l3 := findLayer(r, "L3")
	if l3.Score != 1 {
		for _, c := range l3.Checks {
			t.Logf("L3 check %s: %s score=%v detail=%s", c.Name, c.Status, c.Score, c.Detail)
		}
		t.Fatalf("L3 应满分, score=%v", l3.Score)
	}

	// L4/L5 有得分且关键指标存在
	l5 := findLayer(r, "L5")
	probe := findCheck(l5, "context_probe")
	if probe == nil || probe.Metrics["max_context_ok"].(int) != 16384 {
		t.Fatalf("context_probe 应在 32768 处停止, metrics=%v", probe)
	}

	if r.Verdict != "pass" {
		t.Fatalf("总评应为 pass, 实际 %s（TotalScore=%.2f）", r.Verdict, r.TotalScore)
	}
	if r.TotalScore < 0.8 {
		t.Fatalf("总分偏低: %v", r.TotalScore)
	}
}

func TestRunPipelineGateAbort(t *testing.T) {
	srv := newMockServer(t)
	cfg, err := config.Load(writeTestConfig(t, srv.URL), filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatal(err)
	}
	srv.Close() // 服务不可达

	r, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run 不应返回错误（应产出 abort 报告）: %v", err)
	}
	if r.Verdict != "abort" {
		t.Fatalf("L1 失败时结论应为 abort, 实际 %s", r.Verdict)
	}
	for _, id := range []string{"L2", "L3", "L4", "L5"} {
		if l := findLayer(r, id); l == nil || !l.Skipped {
			t.Fatalf("层 %s 应被跳过", id)
		}
	}
}
