package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EnforceModelPolicy max_tokens 治理表驱动测试：reasoning floor（裂缝 2）+
// DB 权威上限 clamp（裂缝 3）+ 0 注入 DB 值（spec §4 L1）。原
// applyMaxTokensPolicy 静态目录逻辑已迁入 DB policy 预计算。
func TestEnforceModelPolicy_MaxTokensGovernance(t *testing.T) {
	tests := []struct {
		name       string
		policy     *ModelPolicy
		reasoning  bool
		maxTokens  int
		wantTokens int
		// mutated 标记策略是否应当返回 clone；无变化时须返回原指针。
		mutated bool
	}{
		// reasoning 显式值低于平台兜底 → floor 抬升（裂缝 2 主场景）。
		{name: "reasoning explicit below floor raised", policy: &ModelPolicy{}, reasoning: true, maxTokens: 1024, wantTokens: constants.DefaultOutputReserveTokens, mutated: true},
		// 非 reasoning 显式值低于兜底 → 不动（floor 仅 reasoning）。
		{name: "non-reasoning explicit below floor untouched", policy: &ModelPolicy{}, reasoning: false, maxTokens: 1024, wantTokens: 1024},
		// reasoning 显式值超 DB 上限 → clamp（裂缝 3 主场景，qwq-32b maxOut 8192）。
		{name: "reasoning explicit above maxOut clamped", policy: &ModelPolicy{MaxTokens: 8192}, reasoning: true, maxTokens: 20000, wantTokens: 8192, mutated: true},
		// 非 reasoning 显式值超上限 → clamp（family 前缀命中，mistral-small 4096）。
		{name: "non-reasoning explicit above maxOut clamped", policy: &ModelPolicy{MaxTokens: 4096}, reasoning: false, maxTokens: 8192, wantTokens: 4096, mutated: true},
		// floor 抬升后仍超上限 → clamp 优先（能力是硬约束）。
		{name: "floor then clamp prefers hard limit", policy: &ModelPolicy{MaxTokens: 8192}, reasoning: true, maxTokens: 100000, wantTokens: 8192, mutated: true},
		// 区间内显式值 → 原样返回。
		{name: "explicit within range untouched", policy: &ModelPolicy{MaxTokens: 8192}, reasoning: true, maxTokens: 6000, wantTokens: 6000},
		// 权威数据不存在（policy nil）→ 不做任何治理（接线层短路）。
		{name: "policy nil passthrough", policy: nil, reasoning: false, maxTokens: 8192, wantTokens: 8192},
		// 目录未知 + reasoning → floor 仍生效（floor 不依赖 maxOut）。
		{name: "unknown reasoning model floor still applies", policy: &ModelPolicy{}, reasoning: true, maxTokens: 1024, wantTokens: constants.DefaultOutputReserveTokens, mutated: true},
		// 0 = 未设置 → 仅注入独立默认输出，不能把能力上限当默认预算。
		{name: "unset injects default output", policy: &ModelPolicy{MaxTokens: 8192, DefaultOutputTokens: 2048}, reasoning: true, maxTokens: 0, wantTokens: 2048, mutated: true},
		// 0 = 未设置且 DB 上限未知（0）→ 保持 0（协议层兜底语义不动）。
		{name: "unset with unknown max_tokens passthrough", policy: &ModelPolicy{}, reasoning: true, maxTokens: 0, wantTokens: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// req.Model 与 link.Model 不同：模拟 fallback 候选链路。
			req := &CompletionRequest{Model: "primary", MaxTokens: tc.maxTokens}

			got, err := EnforceModelPolicy(req, tc.policy, tc.reasoning)
			require.NoError(t, err)
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
