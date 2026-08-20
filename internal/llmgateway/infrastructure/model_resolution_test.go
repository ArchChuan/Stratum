package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// TestGatewayComplete_emptyModelUsesResolvedDefault 验证空模型请求必须携带
// 解析出的默认模型名（修复 "model": "" 导致 provider 400 的缺陷）。默认模型
// 只允许来自模型目录/Provider 配置，代码内禁止写死兜底模型。
func TestGatewayComplete_emptyModelUsesResolvedDefault(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{"primary": {}})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	resp, err := f.gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: ""})
	require.NoError(t, err)
	require.Equal(t, "primary", resp.ModelResolved)
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

// TestGatewayCompleteStream_emptyModelUsesResolvedDefault 与 Complete 同源：
// 流式路径同样必须携带解析后的模型名。
func TestGatewayCompleteStream_emptyModelUsesResolvedDefault(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{"primary": {}})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := f.gateway.CompleteStream(ctx, &infrastructure.CompletionRequest{Model: ""}, func(string) {})
	require.NoError(t, err)
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

// TestGatewayComplete_invalidExplicitModelFailsClosed 验证显式请求的模型不在
// 目录（或已禁用）时 fail-closed，绝不静默降级到 provider 默认模型——失效
// 模型必须走监控报警链路而非悄悄换模型。
func TestGatewayComplete_invalidExplicitModelFailsClosed(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{"primary": {}})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := f.gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "ghost"})
	require.Error(t, err)
	require.ErrorContains(t, err, `resolve model "ghost"`)
	require.Empty(t, f.proto.callModels(), "无效模型请求不得发起 provider 调用")
}

// resolutionSpyFixture 构造带指标 spy 的 gateway（单 provider + 默认模型
// "primary"），用于断言模型解析失败进入监控链路。
func resolutionSpyFixture(scripts map[string]*modelScript) (*infrastructure.Gateway, *llmMetricsSpy) {
	models := []domain.Model{
		{ID: "m-primary", ProviderID: "p1", Name: "primary", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}
	modelRepo := &mockModelRepo{models: models}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "Test Provider", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "primary", Enabled: true,
		},
	}}
	proto := newScriptedProto(scripts)
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}, 5*time.Minute)
	spy := &llmMetricsSpy{}
	gw := infrastructure.NewGateway(reg, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}).WithMetrics(spy)
	return gw, spy
}

// TestGatewayComplete_invalidExplicitModelRecordsAlertMetric 验证显式失效模型
// 必须进入监控报警链路：llm_model_resolution_errors_total{reason=invalid_model}
// + 请求 error 指标。
func TestGatewayComplete_invalidExplicitModelRecordsAlertMetric(t *testing.T) {
	gateway, spy := resolutionSpyFixture(map[string]*modelScript{"primary": {}})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "ghost"})
	require.Error(t, err)
	require.Contains(t, spy.resolutionErrors, "ghost|invalid_model")
	require.Contains(t, spy.requests, "ghost|unknown|error")
}

// TestGatewayComplete_emptyModelWithoutDefaultRecordsAlertMetric 验证目录无任何
// 默认/推荐模型时进入监控报警链路：reason=no_default。
func TestGatewayComplete_emptyModelWithoutDefaultRecordsAlertMetric(t *testing.T) {
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: &successChatProto{}}
	reg := infrastructure.NewModelRegistry(
		&mockModelRepo{},
		&mockProviderRepo{},
		chatProtos,
		map[domain.ProviderKind]infrastructure.EmbedProtocol{},
		5*time.Minute,
	)
	spy := &llmMetricsSpy{}
	gateway := infrastructure.NewGateway(reg, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}).WithMetrics(spy)
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: ""})
	require.Error(t, err)
	require.Contains(t, spy.resolutionErrors, "|no_default")
	require.Contains(t, spy.requests, "|unknown|error")
}

// captureProto 记录请求体（含 response_format），用于验证空模型回填后能力
// 门控按真实模型计算。
type captureProto struct {
	got *infrastructure.CompletionRequest
}

func (c *captureProto) Complete(_ context.Context, _ infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	c.got = req
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}
func (c *captureProto) CompleteStream(context.Context, infrastructure.ProviderConfig, *infrastructure.CompletionRequest, func(string)) (*infrastructure.CompletionResponse, error) {
	return nil, nil
}
func (c *captureProto) Health(context.Context, infrastructure.ProviderConfig) error { return nil }
func (c *captureProto) ListModels(context.Context, infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

// TestGatewayComplete_emptyModelKeepsStructuredOutputForResolvedModel 验证空模型
// 回填默认模型（glm-4.5）后，能力门控按回填模型计算：response_format 不被清空，
// 结构化抽取链路不受影响。
func TestGatewayComplete_emptyModelKeepsStructuredOutputForResolvedModel(t *testing.T) {
	models := []domain.Model{
		{ID: "m-default", ProviderID: "p1", Name: "glm-4.5", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapToolUse}},
	}
	modelRepo := &mockModelRepo{models: models}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "Test Provider", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "glm-4.5", Enabled: true,
		},
	}}
	proto := &captureProto{}
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}, 5*time.Minute)
	gateway := infrastructure.NewGateway(reg, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{})
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{
		Model:          "",
		ResponseFormat: &domain.ResponseFormat{Type: "json_object"},
	})
	require.NoError(t, err)
	require.Equal(t, "glm-4.5", proto.got.Model)
	require.NotNil(t, proto.got.ResponseFormat, "空模型回填后必须按默认模型保留 response_format")
}
