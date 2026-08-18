package infrastructure_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

func floatPtr(v float64) *float64 { return &v }

// TestEnforceModelPolicy_clampThenInjectThenValidate 覆盖 spec §12 执行顺序：
// clamp → 注入 → 预检 → 校验。注入值越界必须被 L3 拒绝——请求未设
// temperature，注入模型默认 0.8 超 max_temperature 0.5 → 拒。
func TestEnforceModelPolicy_clampThenInjectThenValidate(t *testing.T) {
	p := &infrastructure.ModelPolicy{
		MaxTokens: 8192, ContextWindow: 32768,
		MaxTemperature:   floatPtr(0.5),
		SamplingDefaults: &domain.SamplingParams{Temperature: floatPtr(0.8)},
	}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 20000} // 无显式 temperature
	got, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.ErrorIs(t, err, domain.ErrSamplingOutOfRange)
	require.Nil(t, got)
}

func TestEnforceModelPolicy_l1Clamp(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 32768}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 20000}
	got, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.NoError(t, err)
	require.Equal(t, 8192, got.MaxTokens)
}

func TestEnforceModelPolicy_injectModelSampling(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192,
		SamplingDefaults: &domain.SamplingParams{Temperature: floatPtr(0.7)}}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
	got, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.NoError(t, err)
	require.Equal(t, 0.7, *got.Temperature)
}

func TestEnforceModelPolicy_l2WindowExceeded(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 4096}
	req := &infrastructure.CompletionRequest{
		Model: "m", MaxTokens: 100,
		Messages: []infrastructure.Message{{Role: "user", Content: strings.Repeat("x", 4096*3)}},
	}
	_, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.ErrorIs(t, err, infrastructure.ErrContextLengthExceeded)
	require.True(t, infrastructure.IsContextLengthExceeded(err))
}

func TestEnforceModelPolicy_windowUnknownSkips(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 0}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
	got, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestEnforceModelPolicy_l3SamplingOutOfRange(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, MaxTemperature: floatPtr(0.5)}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Temperature: floatPtr(0.9)}
	_, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.ErrorIs(t, err, domain.ErrSamplingOutOfRange)
}

// L4 known-non：能力集非空且明确不含 tool_use → 拒（spec §4 fail-closed）。
func TestEnforceModelPolicy_l4ToolUseKnownNon(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192,
		Capabilities: []domain.ModelCapability{domain.CapChat}} // 显式能力集，无 tool_use
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Tools: []domain.Tool{{Type: "function"}}}
	_, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.ErrorIs(t, err, domain.ErrCapabilityUnsupported)
}

// L4 unknown 放行：空能力集 = unknown（数据未维护），与 policy nil 同构放行。
func TestEnforceModelPolicy_l4ToolUseUnknownAllows(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, Capabilities: []domain.ModelCapability{domain.CapToolUse}}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Tools: []domain.Tool{{Type: "function"}}}
	got, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestEnforceModelPolicy_reasoningFloorBeforeClamp(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
	got, err := infrastructure.EnforceModelPolicy(req, p, true) // reasoning
	require.NoError(t, err)
	require.Equal(t, 4096, got.MaxTokens) // constants.DefaultOutputReserveTokens
}

// 拦截错误语义化：permanent（不重试不降级）+ 不污染原请求。
func TestEnforceModelPolicy_blockedErrorIsPermanent(t *testing.T) {
	p := &infrastructure.ModelPolicy{MaxTokens: 8192, MaxTemperature: floatPtr(0.5)}
	req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Temperature: floatPtr(0.9)}
	_, err := infrastructure.EnforceModelPolicy(req, p, false)
	require.True(t, infrastructure.IsPermanent(err))
	require.Equal(t, 0.9, *req.Temperature) // 原请求未被修改
}
