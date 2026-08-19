package infrastructure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// captureChatProto 捕获协议层收到的最终请求，验证 invoke 链中 policy 已接线。
type captureChatProto struct {
	req *infrastructure.CompletionRequest
}

func (c *captureChatProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	c.req = req
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}
func (c *captureChatProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	return nil, errors.New("not used")
}
func (c *captureChatProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error {
	return nil
}
func (c *captureChatProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

// 端到端：显式值超 qwen-turbo 静态上限（8192）时，协议层实际收到 clamp 后的值。
func TestGatewayComplete_appliesMaxTokensPolicy(t *testing.T) {
	proto := &captureChatProto{}
	gateway, _, _ := gatewayFixture(proto, successEmbedProto{})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "qwen-turbo", MaxTokens: 20000})
	require.NoError(t, err)
	require.NotNil(t, proto.req)
	require.Equal(t, 8192, proto.req.MaxTokens)
}

// 端到端：未设置时不把模型能力上限误当作默认输出预算。
func TestGatewayComplete_policyDoesNotInjectModelMaxTokens(t *testing.T) {
	proto := &captureChatProto{}
	gateway, _, _ := gatewayFixture(proto, successEmbedProto{})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "qwen-turbo"})
	require.NoError(t, err)
	require.NotNil(t, proto.req)
	require.Zero(t, proto.req.MaxTokens)
}
