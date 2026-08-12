package infrastructure_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// effortScriptedProto 记录每次调用的 "model:reasoning_effort"，并按模型分派
// 错误（复用 modelScript：completeSeq 逐次消费，耗尽后成功）。
type effortScriptedProto struct {
	mu      sync.Mutex
	calls   []string
	scripts map[string]*modelScript
}

func (p *effortScriptedProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model+":"+req.ReasoningEffort)
	s := p.scripts[req.Model]
	var callErr error
	if s != nil {
		if len(s.completeSeq) > 0 {
			callErr = s.completeSeq[0]
			s.completeSeq = s.completeSeq[1:]
		} else {
			callErr = s.completeErr
		}
	}
	p.mu.Unlock()
	if callErr != nil {
		return nil, callErr
	}
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}

func (p *effortScriptedProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model+":"+req.ReasoningEffort)
	p.mu.Unlock()
	onToken("t")
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}
func (p *effortScriptedProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error {
	return nil
}
func (p *effortScriptedProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

func (p *effortScriptedProto) callModels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// reasoningGateway 装配一个单 provider 的网关，模型集由 models 指定。
func reasoningGateway(models []domain.Model, proto infrastructure.ChatProtocol) *infrastructure.Gateway {
	providerRepo := &mockProviderRepo{
		providers: map[string]*domain.Provider{
			"p1": {ID: "p1", Name: "Test Provider", Kind: domain.ProviderOpenAICompat,
				BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: models[0].Name, Enabled: true},
		},
	}
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(&mockModelRepo{models: models}, providerRepo, chatProtos, nil, 5*time.Minute)
	return infrastructure.NewGateway(reg, chatProtos, nil)
}

func TestGatewayReasoningEffort_knownReasoningPassthrough(t *testing.T) {
	proto := &effortScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "o3-mini", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "o3-mini", ReasoningEffort: "high"})
	require.NoError(t, err)
	require.Equal(t, []string{"o3-mini:high"}, proto.callModels())
}

func TestGatewayReasoningEffort_knownNonReasoningClearedAndWarned(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	proto := &effortScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.New(core))
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "qwen-turbo", ReasoningEffort: "high"})
	require.NoError(t, err)
	// 非推理模型：effort 被清空，请求体不携带 reasoning_effort。
	require.Equal(t, []string{"qwen-turbo:"}, proto.callModels())
	require.Equal(t, 1, logs.FilterMessage("llmgateway: reasoning_effort ignored for non-reasoning model").Len())
}

func TestGatewayReasoningEffort_catalogFallbackWhenDBCapabilityAbsent(t *testing.T) {
	// DB 未打 CapReasoning，但 catalog 已知 deepseek-reasoner 是推理模型
	// （DB∨catalog 并集）：effort 仍应透传。
	proto := &effortScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "deepseek-reasoner", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "deepseek-reasoner", ReasoningEffort: "medium"})
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-reasoner:medium"}, proto.callModels())
}

func TestGatewayReasoningEffort_fallbackCandidateCleared(t *testing.T) {
	// 主模型推理瞬态失败 → 降级到非推理候选：候选尝试必须清空 effort，
	// 否则严格端点 400（永久错误）会中止整条 fallback 链。
	proto := &effortScriptedProto{scripts: map[string]*modelScript{
		"o3-mini":    {completeErr: &statusErr{code: 500}},
		"qwen-turbo": {},
	}}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "o3-mini", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning}},
		{ID: "m2", ProviderID: "p1", Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	resp, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "o3-mini", ReasoningEffort: "high"})
	require.NoError(t, err)
	require.Equal(t, "qwen-turbo", resp.ModelResolved)
	// 主模型：首次 + 立即重试均带 effort；候选：清空。
	require.Equal(t, []string{"o3-mini:high", "o3-mini:high", "qwen-turbo:"}, proto.callModels())
}

func TestGatewayReasoningEffort_streamKnownReasoningPassthrough(t *testing.T) {
	proto := &effortScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "o3-mini", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.CompleteStream(ctx, &infrastructure.CompletionRequest{Model: "o3-mini", ReasoningEffort: "low"}, func(string) {})
	require.NoError(t, err)
	require.Equal(t, []string{"o3-mini:low"}, proto.callModels())
}
