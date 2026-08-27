package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestInjectSystemInstruction 验证"声称带引用"规则块锚入首条 system 消息之后
// （头部 anchor 区压缩不逐出）；无 system 消息时置于最前。
func TestInjectSystemInstruction(t *testing.T) {
	rule := "citation rule"
	base := []port.LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}

	t.Run("injects after first system message", func(t *testing.T) {
		out := injectSystemInstruction(base, rule)
		if len(out) != 3 {
			t.Fatalf("len = %d, want 3", len(out))
		}
		if out[0].Content != "sys" || out[1].Content != rule || out[2].Content != "hi" {
			t.Fatalf("rule must sit after first system message: %+v", out)
		}
		if out[1].Role != "system" {
			t.Fatalf("rule role = %q, want system", out[1].Role)
		}
	})

	t.Run("no system message puts rule first", func(t *testing.T) {
		out := injectSystemInstruction([]port.LLMMessage{{Role: "user", Content: "hi"}}, rule)
		if len(out) != 2 || out[0].Content != rule {
			t.Fatalf("rule must lead when no system message: %+v", out)
		}
	})
}
