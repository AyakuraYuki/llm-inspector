package params

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMessageSerialization 锁定 Message 的 JSON/YAML tag 契约
// （该类型被 config 的 yaml 反序列化使用）。
func TestMessageSerialization(t *testing.T) {
	m := Message{
		Role:       "assistant",
		Content:    "hi",
		Name:       "fn",
		ToolCallID: "call-1",
		ToolCalls:  []ToolCall{{ID: "call-1", Name: "fn", Arguments: `{"a":1}`}},
	}

	bs, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var gotJSON map[string]any
	if err := json.Unmarshal(bs, &gotJSON); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if gotJSON["tool_call_id"] != "call-1" {
		t.Errorf("tool_call_id = %v, want call-1", gotJSON["tool_call_id"])
	}
	if _, ok := gotJSON["tool_calls"]; !ok {
		t.Error("tool_calls 缺失")
	}

	bs, err = yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var gotYAML map[string]any
	if err := yaml.Unmarshal(bs, &gotYAML); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if gotYAML["tool_call_id"] != "call-1" {
		t.Errorf("yaml tool_call_id = %v, want call-1", gotYAML["tool_call_id"])
	}
	if gotYAML["role"] != "assistant" {
		t.Errorf("yaml role = %v, want assistant", gotYAML["role"])
	}
}

// TestToolCallSerialization 锁定 ToolCall 的 JSON tag 契约。
func TestToolCallSerialization(t *testing.T) {
	bs, err := json.Marshal(ToolCall{ID: "call-1", Name: "fn", Arguments: `{"a":1}`})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(bs, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got["name"] != "fn" || got["arguments"] != `{"a":1}` || got["id"] != "call-1" {
		t.Errorf("ToolCall JSON = %v", got)
	}
}
