package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// applyMaxTokensPolicy 表驱动测试：reasoning floor（裂缝 2）+ 已知上限 clamp（裂缝 3），
// 未知模型透传、0 不动（协议层语义）、原请求不被污染。
func TestApplyMaxTokensPolicy(t *testing.T) {
	gateway := &Gateway{logger: zap.NewNop()}

	tests := []struct {
		name       string
		model      string
		reasoning  bool
		maxTokens  int
		wantTokens int
		// mutated 标记策略是否应当返回 clone；无变化时须返回原指针。
		mutated bool
	}{
		// reasoning 显式值低于平台兜底 → floor 抬升（裂缝 2 主场景）。
		{name: "reasoning explicit below floor raised", model: "qwq-32b", reasoning: true, maxTokens: 1024, wantTokens: constants.DefaultOutputReserveTokens, mutated: true},
		// 非 reasoning 显式值低于兜底 → 不动（floor 仅 reasoning）。
		{name: "non-reasoning explicit below floor untouched", model: "mistral-small", reasoning: false, maxTokens: 1024, wantTokens: 1024},
		// reasoning 显式值超模型上限 → clamp（裂缝 3 主场景）。
		{name: "reasoning explicit above maxOut clamped", model: "qwq-32b", reasoning: true, maxTokens: 20000, wantTokens: 8192, mutated: true},
		// 非 reasoning 显式值超上限 → clamp（family 前缀命中）。
		{name: "non-reasoning explicit above maxOut clamped", model: "mistral-small", reasoning: false, maxTokens: 8192, wantTokens: 4096, mutated: true},
		// floor 抬升后仍超上限 → clamp 优先（能力是硬约束）。
		{name: "floor then clamp prefers hard limit", model: "qwq-32b", reasoning: true, maxTokens: 100000, wantTokens: 8192, mutated: true},
		// 区间内显式值 → 原样返回。
		{name: "explicit within range untouched", model: "qwq-32b", reasoning: true, maxTokens: 6000, wantTokens: 6000},
		// 目录未知 + 非推理 → 透传（0/0），provider 400 即反馈。
		{name: "unknown model passthrough", model: "private-model", reasoning: false, maxTokens: 8192, wantTokens: 8192},
		// 目录未知 + reasoning → floor 仍生效（floor 不依赖 maxOut）。
		{name: "unknown reasoning model floor still applies", model: "private-reasoner", reasoning: true, maxTokens: 1024, wantTokens: constants.DefaultOutputReserveTokens, mutated: true},
		// 0 = 未设置 → 不动：语义属协议层（OpenAI/Anthropic 兜底 4096，ollama 无限）。
		{name: "unset passthrough", model: "qwq-32b", reasoning: true, maxTokens: 0, wantTokens: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// req.Model 与 link.Model 不同：模拟 fallback 候选链路。
			req := &CompletionRequest{Model: "primary", MaxTokens: tc.maxTokens}
			link := chainLink{Model: tc.model, Reasoning: tc.reasoning}

			got := gateway.applyMaxTokensPolicy(req, link)
			require.Equal(t, tc.wantTokens, got.MaxTokens)
			// 副本语义：原请求永不修改，变化时返回新指针。
			require.Equal(t, tc.maxTokens, req.MaxTokens)
			if tc.mutated {
				require.NotSame(t, req, got)
			} else {
				require.Same(t, req, got)
			}
		})
	}
}
