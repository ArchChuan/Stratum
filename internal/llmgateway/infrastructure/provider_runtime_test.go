package infrastructure

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/stretchr/testify/require"
)

var errUpstream = errors.New("upstream failed")

type stubProto struct {
	models []DiscoveredModel
	err    error
}

func (s *stubProto) Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
	return nil, s.err
}

func (s *stubProto) CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	return nil, s.err
}

func (s *stubProto) Health(ctx context.Context, cfg ProviderConfig) error {
	return s.err
}

func (s *stubProto) ListModels(ctx context.Context, cfg ProviderConfig) ([]DiscoveredModel, error) {
	return s.models, s.err
}

func runtimeFixture(stub *stubProto) *ProviderRuntime {
	return NewProviderRuntime(map[domain.ProviderKind]ChatProtocol{domain.ProviderOpenAICompat: stub})
}

func TestProviderRuntime_ListModels_success(t *testing.T) {
	stub := &stubProto{models: []DiscoveredModel{{Name: "qwen-turbo", ContextWindow: 8192, MaxOutputTokens: 4096}}}
	rt := runtimeFixture(stub)

	models, err := rt.ListModels(context.Background(), domain.Provider{Kind: domain.ProviderOpenAICompat})
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "qwen-turbo", models[0].Name)
	require.Equal(t, 8192, models[0].ContextWindow)
}

func TestProviderRuntime_ListModels_empty(t *testing.T) {
	rt := runtimeFixture(&stubProto{})

	models, err := rt.ListModels(context.Background(), domain.Provider{Kind: domain.ProviderOpenAICompat})
	require.NoError(t, err)
	require.Empty(t, models) // non-nil: make() over 0-length slice
	require.NotNil(t, models)
}

func TestProviderRuntime_ListModels_protocolError(t *testing.T) {
	stub := &stubProto{err: errUpstream}
	rt := runtimeFixture(stub)

	_, err := rt.ListModels(context.Background(), domain.Provider{Kind: domain.ProviderOpenAICompat})
	require.ErrorIs(t, err, errUpstream)
}

func TestProviderRuntime_Health_successAndError(t *testing.T) {
	rt := runtimeFixture(&stubProto{})
	require.NoError(t, rt.Health(context.Background(), domain.Provider{Kind: domain.ProviderOpenAICompat}))

	stub := &stubProto{err: errUpstream}
	rt = runtimeFixture(stub)
	require.ErrorIs(t, rt.Health(context.Background(), domain.Provider{Kind: domain.ProviderOpenAICompat}), errUpstream)
}

func TestProviderRuntime_unsupportedKind(t *testing.T) {
	rt := runtimeFixture(&stubProto{})

	provider := domain.Provider{Kind: domain.ProviderKind("kafka")}
	_, err := rt.ListModels(context.Background(), provider)
	require.ErrorContains(t, err, `no protocol for kind "kafka"`)
	require.ErrorContains(t, rt.Health(context.Background(), provider), `no protocol for kind "kafka"`)
}

// TestProviderRuntime_protocol_injectsZhipuCatalog 验证 protocol() 对智谱
// baseURL 注入发现兜底目录，非智谱 provider 保持为空（行为不变）。
func TestProviderRuntime_protocol_injectsZhipuCatalog(t *testing.T) {
	rt := runtimeFixture(&stubProto{})

	_, cfg, err := rt.protocol(domain.Provider{
		Kind: domain.ProviderOpenAICompat, BaseURL: "https://open.bigmodel.cn/api/paas/v4",
	})
	require.NoError(t, err)
	require.NotEmpty(t, cfg.ModelCatalog)
	require.Contains(t, cfg.ModelCatalog, "glm-4.6v")
	require.Contains(t, cfg.ModelCatalog, "embedding-3")
	require.NotContains(t, cfg.ModelCatalog, "glm-4.1v")

	_, cfg, err = rt.protocol(domain.Provider{
		Kind: domain.ProviderOpenAICompat, BaseURL: "https://api.example.com/v1",
	})
	require.NoError(t, err)
	require.Empty(t, cfg.ModelCatalog)
}
