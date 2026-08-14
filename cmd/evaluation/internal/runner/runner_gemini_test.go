package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/config"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// validateGeminiParams 模拟 Gemini 标准实现的参数校验：类型错误与越界返回错误消息。
func validateGeminiParams(raw map[string]any) string {
	contents, ok := raw["contents"].([]any)
	if !ok || len(contents) == 0 {
		return "contents must be a non-empty array"
	}
	for _, c := range contents {
		obj, isObj := c.(map[string]any)
		if !isObj {
			return "content must be an object"
		}
		if role, has := obj["role"].(string); has && role != "user" && role != "model" {
			return "invalid content role"
		}
		if p, present := obj["parts"]; present && p == nil {
			return "parts must not be null"
		}
	}
	gc, _ := raw["generationConfig"].(map[string]any)
	if gc == nil {
		return ""
	}
	numInRange := func(key string, lo, hi float64) string {
		v, ok := gc[key]
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
	if msg := numInRange("topP", 0, 1); msg != "" {
		return msg
	}
	if msg := numInRange("temperature", 0, 2); msg != "" {
		return msg
	}
	if v, ok := gc["maxOutputTokens"]; ok && v != nil {
		f, isNum := v.(float64)
		if !isNum {
			return "maxOutputTokens must be an integer"
		}
		if f <= 0 || f > 1e8 {
			return "maxOutputTokens out of range"
		}
	}
	return ""
}

// newGeminiE2EServer 是支持 L1+L2 全部检查的 Gemini 协议 mock。
func newGeminiE2EServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("x-goog-api-key") != "Bearer-good" && r.Header.Get("x-goog-api-key") != "test-key" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"API key not valid.","status":"INVALID_ARGUMENT"}}`)
			return false
		}
		return true
	}

	mux.HandleFunc("/v1beta/models", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = fmt.Fprint(w, `{"models":[{"name":"models/gemini-mock"}]}`)
	})

	mux.HandleFunc("/v1beta/models/", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, "/v1beta/models/")
		model, action, _ := strings.Cut(tail, ":")
		if model == "nonexistent-model-00000000" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"error":{"code":404,"message":"not found","status":"NOT_FOUND"}}`)
			return
		}

		body, _ := io.ReadAll(r.Body)
		var rawBody map[string]any
		if err := json.Unmarshal(body, &rawBody); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"bad request","status":"INVALID_ARGUMENT"}}`)
			return
		}
		if msg := validateGeminiParams(rawBody); msg != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = fmt.Fprintf(w, `{"error":{"code":400,"message":%q,"status":"INVALID_ARGUMENT"}}`, msg)
			return
		}

		var req struct {
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text             string         `json:"text"`
					FunctionResponse map[string]any `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
			SystemInstruction *struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"systemInstruction"`
			GenerationConfig *struct {
				MaxOutputTokens  int            `json:"maxOutputTokens"`
				Temperature      float64        `json:"temperature"`
				ResponseMIMEType string         `json:"responseMimeType"`
				ResponseSchema   map[string]any `json:"responseSchema"`
				StopSequences    []string       `json:"stopSequences"`
			} `json:"generationConfig"`
			Tools []map[string]any `json:"tools"`
		}
		_ = json.Unmarshal(body, &req)

		last := ""
		hasFunctionResponse := false
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.FunctionResponse != nil {
					hasFunctionResponse = true
				}
			}
		}
		for i := len(req.Contents) - 1; i >= 0; i-- {
			if req.Contents[i].Role != "model" && len(req.Contents[i].Parts) > 0 {
				last = req.Contents[i].Parts[len(req.Contents[i].Parts)-1].Text
				break
			}
		}
		maxOut := 0
		jsonMode := false
		hasSchema := false
		var stops []string
		if req.GenerationConfig != nil {
			maxOut = req.GenerationConfig.MaxOutputTokens
			jsonMode = req.GenerationConfig.ResponseMIMEType == "application/json"
			hasSchema = req.GenerationConfig.ResponseSchema != nil
			stops = req.GenerationConfig.StopSequences
		}
		usage := `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}`

		answer := geminiRoute(req.SystemInstruction != nil && len(req.SystemInstruction.Parts) > 0 &&
			strings.Contains(req.SystemInstruction.Parts[0].Text, "评测前缀"), last)
		for _, s := range stops {
			if i := strings.Index(answer, s); i >= 0 {
				answer = answer[:i]
			}
		}
		finish := "STOP"
		if maxOut > 0 && maxOut <= 16 {
			finish = "MAX_TOKENS"
		}

		write := func(ans, fr string, extraParts string) {
			_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[%s]},"finishReason":%q}],%s}`,
				extraParts+`{"text":`+quote(ans)+`}`, fr, usage)
		}

		// 工具结果回传后的二次推理
		if hasFunctionResponse {
			write("巴黎现在气温 19°C，天气晴。", "STOP", "")
			return
		}

		// 工具调用（含并行场景）
		if len(req.Tools) > 0 {
			toolsJSON, _ := json.Marshal(req.Tools)
			if strings.Contains(string(toolsJSON), "get_time") && strings.Contains(last, "同时") {
				_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[`+
					`{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}},`+
					`{"functionCall":{"name":"get_time","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],%s}`, usage)
				return
			}
			_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],%s}`, usage)
			return
		}

		// json_schema / JSON mode
		if hasSchema {
			write(`{"city":"Paris","country":"France","tags":["capital","art"]}`, "STOP", "")
			return
		}
		if jsonMode {
			write(`{"city":"Paris"}`, "STOP", "")
			return
		}

		// 流式
		if action == "streamGenerateContent" {
			w.Header().Set("Content-Type", "text/event-stream")
			half := len(answer) / 2
			_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":%s}]}}]}\n\n",
				quote(answer[:half]))
			_, _ = fmt.Fprintf(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":%s}]},\"finishReason\":%q}],%s}\n\n",
				quote(answer[half:]), finish, usage)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		write(answer, finish, "")
	})

	return httptest.NewServer(mux)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func geminiRoute(hasPrefixSys bool, last string) string {
	if hasPrefixSys {
		return "评测前缀：你好，很高兴见到你。"
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
	case strings.Contains(last, "长诗"):
		return "秋风起兮白云飞，草木黄落兮雁南归。"
	case strings.Contains(last, "1 到 1000000 之间的整数"):
		return "42"
	case strings.Contains(last, "从 1 数到 10"):
		return "1 2 3 4 5 6 7 8 9 10"
	case strings.Contains(last, "暗号"):
		return "蓝色狐狸"
	case strings.Contains(last, "ping"):
		return "pong"
	case strings.Contains(last, "你好"):
		return "你好"
	default:
		return "OK"
	}
}

func TestRunPipelineGeminiProtocol(t *testing.T) {
	srv := newGeminiE2EServer(t)
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "eval.yaml")
	disabled := "enabled: false"
	cfgContent := fmt.Sprintf(`
target:
  base_url: %s
  api_key: test-key
  model: gemini-mock
  protocol: gemini
  timeout: 10s
  constraints:
    specified_temperature: 1.0
layers:
  capability: {%s}
  stability: {%s}
  performance: {%s}
thresholds: {min_layer_score: 0.6}
`, srv.URL, disabled, disabled, disabled)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath, filepath.Base(os.Args[0]))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	l1 := findLayer(r, "L1")
	if l1 == nil || !l1.Passed || l1.Score != 1 {
		t.Fatalf("Gemini L1 应满分通过, got %+v", l1)
	}

	l2 := findLayer(r, "L2")
	if l2 == nil || l2.Skipped {
		t.Fatal("Gemini L2 应执行")
	}
	for _, c := range l2.Checks {
		// 未配置约束的检查项（thinking/reasoning_effort/default_max_tokens 等）跳过不计
		if c.Status == types.StatusSkip || c.Status == types.StatusUnsupported {
			continue
		}
		if c.Status != types.StatusPass || c.Score < 1 {
			t.Errorf("L2/%s 应满分通过: status=%s score=%v detail=%s",
				c.Name, c.Status, c.Score, c.Detail)
		}
	}

	// L3-L5 已禁用
	for _, id := range []string{"L3", "L4", "L5"} {
		if l := findLayer(r, id); l == nil || l.Enabled {
			t.Errorf("层 %s 应未启用", id)
		}
	}

	if r.Target.Protocol != "gemini" {
		t.Errorf("报告 protocol = %q, want gemini", r.Target.Protocol)
	}
	if r.Verdict != "pass" {
		t.Fatalf("结论应为 pass, 实际 %s", r.Verdict)
	}
}
