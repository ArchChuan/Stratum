package domain

import (
	"math"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestJSONObject(t *testing.T) {
	if got := JSONObject(); got == nil || got.Type != "json_object" {
		t.Fatalf("JSONObject() = %#v, want type json_object", got)
	}
}

func TestNewChatRequestPassesThrough(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hi"}}
	tools := []Tool{{Function: ToolFunction{Name: "search"}}}
	req := NewChatRequest("qwen-max", msgs, tools, constants.ReasoningEffortHigh)

	if req.Model != "qwen-max" {
		t.Fatalf("Model = %q, want qwen-max", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
		t.Fatalf("Messages = %#v, want passthrough", req.Messages)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search" {
		t.Fatalf("Tools = %#v, want passthrough", req.Tools)
	}
	if req.ReasoningEffort != constants.ReasoningEffortHigh {
		t.Fatalf("ReasoningEffort = %q, want %q", req.ReasoningEffort, constants.ReasoningEffortHigh)
	}
	if req.Temperature != nil || req.ResponseFormat != nil || req.NoPrimaryRetry {
		t.Fatalf("chat request must be passthrough with zero task defaults: %#v", req)
	}
}

func TestNewSummarizeRequest(t *testing.T) {
	req := NewSummarizeRequest("qwen-turbo", "压缩：", []string{"a", "b"}, 512)

	if req.Model != "qwen-turbo" {
		t.Fatalf("Model = %q, want qwen-turbo", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("Messages must be single user message: %#v", req.Messages)
	}
	if req.Messages[0].Content != "压缩：a\nb" {
		t.Fatalf("Content = %q, want instructions + items joined", req.Messages[0].Content)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Fatalf("Temperature = %v, want summarize default 0.2 (rounded)", req.Temperature)
	}
	if req.MaxTokens != 512 {
		t.Fatalf("MaxTokens = %d, want 512", req.MaxTokens)
	}
	if !req.NoPrimaryRetry {
		t.Fatalf("summarize must set NoPrimaryRetry (compression path semantics)")
	}
	if req.ResponseFormat != nil || len(req.Tools) != 0 {
		t.Fatalf("summarize must have no response_format / tools: %#v", req)
	}
}

func TestNewSummarizeRequestNilItems(t *testing.T) {
	req := NewSummarizeRequest("m", "指令", nil, 0)
	if req.Messages[0].Content != "指令" {
		t.Fatalf("nil items must keep instructions only, got %q", req.Messages[0].Content)
	}
}

// TestPlatformTemperaturePtrRoundsToTwoDecimals 防智谱等端点对温度 >2 位小数返回 400：
// float32(0.1) 直转 float64 会变成 0.10000000149011612，必须舍入到 0.1。
func TestPlatformTemperaturePtrRoundsToTwoDecimals(t *testing.T) {
	if got := PlatformTemperaturePtr(0.1); got == nil || *got != 0.1 {
		t.Fatalf("PlatformTemperaturePtr(0.1) = %v, want 0.1", got)
	}
	if got := PlatformTemperaturePtr(0); got != nil {
		t.Fatalf("PlatformTemperaturePtr(0) = %v, want nil (unset)", got)
	}
	if got := PlatformTemperaturePtr(0.125); got == nil || *got != 0.13 {
		t.Fatalf("PlatformTemperaturePtr(0.125) = %v, want 0.13", got)
	}
}

func TestNewExtractRequest(t *testing.T) {
	t.Run("with system", func(t *testing.T) {
		req := NewExtractRequest("qwen-plus", "sys", "usr", 0.1, 4096)
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Fatalf("Messages = %#v, want [system, user]", req.Messages)
		}
		// float32(0.1) 转 float64 有精度放大（0.10000000149011612），按 epsilon 近似断言。
		if req.Temperature == nil || math.Abs(*req.Temperature-0.1) > 1e-6 || req.MaxTokens != 4096 {
			t.Fatalf("temp/maxTokens = %v/%d, want 0.1/4096", req.Temperature, req.MaxTokens)
		}
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Fatalf("extract must set json_object response_format: %#v", req.ResponseFormat)
		}
	})
	t.Run("without system", func(t *testing.T) {
		req := NewExtractRequest("m", "", "usr", 0, 0)
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("Messages = %#v, want [user] only", req.Messages)
		}
	})
}
