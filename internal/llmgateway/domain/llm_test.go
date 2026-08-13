package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompletionRequestResponseFormatSerialization 验证 response_format 是 opt-in：
// 显式设置时序列化出现，nil 时完全不出现（向后兼容，Anthropic/Ollama 无此字段）。
func TestCompletionRequestResponseFormatSerialization(t *testing.T) {
	t.Run("json_object present when set", func(t *testing.T) {
		req := CompletionRequest{Model: "qwen-turbo", ResponseFormat: &ResponseFormat{Type: "json_object"}}
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"response_format":{"type":"json_object"}`) {
			t.Fatalf("response_format not serialized: %s", b)
		}
	})

	t.Run("absent when nil", func(t *testing.T) {
		req := CompletionRequest{Model: "qwen-turbo"}
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "response_format") {
			t.Fatalf("response_format must be omitempty: %s", b)
		}
	})
}
