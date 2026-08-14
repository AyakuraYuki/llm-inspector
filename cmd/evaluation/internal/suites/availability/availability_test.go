package availability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/provider"
	"github.com/AyakuraYuki/llm-inspector/cmd/evaluation/internal/types"
)

// newSemanticsServer 构造一个用于错误语义测试的服务：
// 非 "Bearer good-key" 一律 401；模型不存在时按 badModelStatus 返回。
func newSemanticsServer(t *testing.T, badModelStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-key" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "nonexistent-model-00000000" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(badModelStatus)
			fmt.Fprintf(w, `{"error":{"message":"model not available","code":%d}}`, badModelStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","object":"chat.completion","created":1,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"length"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	return httptest.NewServer(mux)
}

func TestCheckErrorSemantics(t *testing.T) {
	tests := []struct {
		name           string
		badModelStatus int
		wantScore      float64
		wantStatus     types.Status
	}{
		{"标准 404 满分", 404, 1, types.StatusPass},
		{"网关 503 半分但通过", 503, 0.75, types.StatusPass},
		{"2xx 未拒绝判 fail", 200, 0.5, types.StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSemanticsServer(t, tt.badModelStatus)
			defer srv.Close()
			good := provider.NewOpenAI(srv.URL+"/v1", "good-key", "m", 5*time.Second)
			bad := provider.NewOpenAI(srv.URL+"/v1", "sk-invalid-key-for-eval", "m", 5*time.Second)

			r := checkErrorSemantics(t.Context(), bad, good)
			if r.Score != tt.wantScore {
				t.Errorf("score = %v, want %v（detail: %s）", r.Score, tt.wantScore, r.Detail)
			}
			if r.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s（detail: %s）", r.Status, tt.wantStatus, r.Detail)
			}
			if r.Metrics["bad_model_status"] != tt.badModelStatus {
				t.Errorf("bad_model_status = %v, want %d", r.Metrics["bad_model_status"], tt.badModelStatus)
			}
		})
	}
}
