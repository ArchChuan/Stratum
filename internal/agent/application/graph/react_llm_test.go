package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestPrepareLLMRequestFinalStepInjectsCitationRule 验证强制收尾步骤
// （Steps 达 MaxLLMSteps-1）按 EnforceClaimCitations 决定是否追加"声称带引用"
// 规则 system 块；未达最终步骤一律不注入。
func TestPrepareLLMRequestFinalStepInjectsCitationRule(t *testing.T) {
	ctx := context.Background()
	messages := []port.LLMMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "hi"},
	}
	hasCitationRule := func(out []port.LLMMessage) bool {
		for _, m := range out {
			if m.Role == "system" && strings.Contains(m.Content, "<tool_ref:") {
				return true
			}
		}
		return false
	}

	t.Run("enforced injects citation rule on final step", func(t *testing.T) {
		s := ReActState{
			Messages:              messages,
			MaxLLMSteps:           2,
			Steps:                 1,
			EnforceClaimCitations: true,
		}
		_, out, _ := prepareLLMRequest(ctx, &s)
		if !hasCitationRule(out) {
			t.Fatal("final step with EnforceClaimCitations must inject citation reference instruction")
		}
	})

	t.Run("not enforced skips citation rule", func(t *testing.T) {
		s := ReActState{
			Messages:    messages,
			MaxLLMSteps: 2,
			Steps:       1,
		}
		_, out, _ := prepareLLMRequest(ctx, &s)
		if hasCitationRule(out) {
			t.Fatal("citation rule must not be injected when EnforceClaimCitations=false")
		}
	})

	t.Run("non-final step no injection", func(t *testing.T) {
		s := ReActState{
			Messages:              messages,
			MaxLLMSteps:           2,
			Steps:                 0,
			EnforceClaimCitations: true,
		}
		_, out, _ := prepareLLMRequest(ctx, &s)
		if hasCitationRule(out) {
			t.Fatal("citation rule must not be injected before the final step")
		}
	})
}
