package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestEffortBudget(t *testing.T) {
	cases := []struct {
		name   string
		effort string
		want   int
	}{
		{name: "low", effort: "low", want: constants.ReasoningEffortBudgetLow},
		{name: "medium", effort: "medium", want: constants.ReasoningEffortBudgetMedium},
		{name: "high", effort: "high", want: constants.ReasoningEffortBudgetHigh},
		{name: "empty unset disables thinking", effort: "", want: 0},
		{name: "unknown value fails closed", effort: "extra", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, effortBudget(tc.effort))
		})
	}
}

// TestBuildRequest_ReasoningEffortThinking 钉住 Anthropic extended_thinking
// 的档位映射与 max_tokens 钳制/抬升：
//   - budget 必须 ≤ max_tokens-reserve（严格端点 400）；默认 max_tokens=4096
//     不足时抬升 max_tokens = budget+reserve；
//   - 显式 max_tokens 充足（如 low+8192）不抬升；
//   - 空串/未知档位不启用 thinking（fail-closed）。
func TestBuildRequest_ReasoningEffortThinking(t *testing.T) {
	client := newAnthropicTestClient()
	const reserve = constants.ReasoningEffortMaxTokensReserve

	cases := []struct {
		name       string
		effort     string
		maxTokens  int
		wantBudget int
		wantMax    int
		wantNil    bool
	}{
		{name: "unset keeps default max_tokens", effort: "", maxTokens: 0,
			wantMax: constants.DefaultOutputReserveTokens, wantNil: true},
		{name: "low default raises max_tokens", effort: "low", maxTokens: 0,
			wantBudget: constants.ReasoningEffortBudgetLow,
			wantMax:    constants.ReasoningEffortBudgetLow + reserve},
		{name: "medium default raises max_tokens", effort: "medium", maxTokens: 0,
			wantBudget: constants.ReasoningEffortBudgetMedium,
			wantMax:    constants.ReasoningEffortBudgetMedium + reserve},
		{name: "high default raises max_tokens", effort: "high", maxTokens: 0,
			wantBudget: constants.ReasoningEffortBudgetHigh,
			wantMax:    constants.ReasoningEffortBudgetHigh + reserve},
		{name: "low explicit 8192 keeps max_tokens", effort: "low", maxTokens: 8192,
			wantBudget: constants.ReasoningEffortBudgetLow, wantMax: 8192},
		{name: "medium explicit 8192 raises", effort: "medium", maxTokens: 8192,
			wantBudget: constants.ReasoningEffortBudgetMedium,
			wantMax:    constants.ReasoningEffortBudgetMedium + reserve},
		{name: "high explicit 8192 raises", effort: "high", maxTokens: 8192,
			wantBudget: constants.ReasoningEffortBudgetHigh,
			wantMax:    constants.ReasoningEffortBudgetHigh + reserve},
		{name: "unknown effort disables thinking", effort: "extra", maxTokens: 8192,
			wantMax: 8192, wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := client.buildRequest(&CompletionRequest{
				Model:           "claude-sonnet-4-5",
				Messages:        []Message{{Role: "user", Content: "hi"}},
				ReasoningEffort: tc.effort,
				MaxTokens:       tc.maxTokens,
			}, false)
			require.Equal(t, tc.wantMax, req.MaxTokens)
			if tc.wantNil {
				require.Nil(t, req.Thinking)
				return
			}
			require.NotNil(t, req.Thinking)
			require.Equal(t, "enabled", req.Thinking.Type)
			require.Equal(t, tc.wantBudget, req.Thinking.BudgetTokens)
		})
	}
}

func TestModelSupportsReasoning(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "o3 is reasoning", model: "o3", want: true},
		{name: "o1-mini is reasoning", model: "o1-mini", want: true},
		{name: "o4-mini is reasoning", model: "o4-mini", want: true},
		{name: "deepseek-reasoner is reasoning", model: "deepseek-reasoner", want: true},
		{name: "qwq-plus is reasoning", model: "qwq-plus", want: true},
		{name: "case insensitive O3", model: "O3", want: true},
		{name: "deepseek-chat is not reasoning", model: "deepseek-chat", want: false},
		{name: "family prefix must not mark deepseek-v4-flash", model: "deepseek-v4-flash", want: false},
		{name: "qwen-plus is not reasoning", model: "qwen-plus", want: false},
		{name: "gpt-4o is not reasoning", model: "gpt-4o", want: false},
		{name: "unknown fails closed", model: "x-unknown-9", want: false},
		{name: "empty is false", model: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ModelSupportsReasoning(tc.model))
		})
	}
}
