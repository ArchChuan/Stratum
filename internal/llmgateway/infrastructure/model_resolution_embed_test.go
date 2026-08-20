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

// captureEmbedProto 记录 embedding 请求体，验证空模型回填后实际发送的模型名。
type captureEmbedProto struct {
	got *infrastructure.EmbeddingRequest
}

func (c *captureEmbedProto) CreateEmbeddings(_ context.Context, _ infrastructure.ProviderConfig, req *infrastructure.EmbeddingRequest) (*infrastructure.EmbeddingResponse, error) {
	c.got = req
	return &infrastructure.EmbeddingResponse{Embeddings: [][]float32{{0.1, 0.2}}}, nil
}
func (c *captureEmbedProto) BatchSize() int { return 8 }

// TestGatewayCreateEmbeddings_emptyModelUsesResolvedDefault 验证 embedding 与
// chat 同源：空模型请求必须携带解析出的默认模型名，禁止请求体发送空模型。
func TestGatewayCreateEmbeddings_emptyModelUsesResolvedDefault(t *testing.T) {
	models := []domain.Model{
		{ID: "m-embed", ProviderID: "p1", Name: "text-embed", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}
	modelRepo := &mockModelRepo{models: models}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{
		"p1": {
			ID: "p1", Name: "Test Embed", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "text-embed", Enabled: true,
		},
	}}
	proto := &captureEmbedProto{}
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(
		modelRepo, providerRepo,
		map[domain.ProviderKind]infrastructure.ChatProtocol{},
		embedProtos, 5*time.Minute,
	)
	gateway := infrastructure.NewGateway(reg, map[domain.ProviderKind]infrastructure.ChatProtocol{}, embedProtos)
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.CreateEmbeddings(ctx, &infrastructure.EmbeddingRequest{Model: "", Input: []string{"hello"}})
	require.NoError(t, err)
	require.Equal(t, "text-embed", proto.got.Model)
}

// TestGatewayCreateEmbeddings_resolveFailureRecordsAlertMetric 验证 embedding
// 解析失败（无默认模型）同样进入监控报警链路。
func TestGatewayCreateEmbeddings_resolveFailureRecordsAlertMetric(t *testing.T) {
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{domain.ProviderOpenAICompat: &captureEmbedProto{}}
	reg := infrastructure.NewModelRegistry(
		&mockModelRepo{},
		&mockProviderRepo{},
		map[domain.ProviderKind]infrastructure.ChatProtocol{},
		embedProtos, 5*time.Minute,
	)
	spy := &llmMetricsSpy{}
	gateway := infrastructure.NewGateway(reg, map[domain.ProviderKind]infrastructure.ChatProtocol{}, embedProtos).WithMetrics(spy)
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.CreateEmbeddings(ctx, &infrastructure.EmbeddingRequest{Model: "", Input: []string{"hello"}})
	require.Error(t, err)
	require.Contains(t, spy.resolutionErrors, "|no_default")
	require.Contains(t, spy.requests, "|unknown|error")
}
