package infrastructure_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// rfScriptedProto 记录每次调用的 "model:response_format类型"（none = 未注入），
// 并按模型分派错误（复用 modelScript：completeSeq 逐次消费，耗尽后成功）。
type rfScriptedProto struct {
	mu      sync.Mutex
	calls   []string
	scripts map[string]*modelScript
}

func (p *rfScriptedProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	p.mu.Lock()
	rf := "none"
	if req.ResponseFormat != nil {
		rf = req.ResponseFormat.Type
	}
	p.calls = append(p.calls, req.Model+":"+rf)
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

func (p *rfScriptedProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model+":stream")
	p.mu.Unlock()
	onToken("t")
	return &infrastructure.CompletionResponse{Content: "ok", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}

func (p *rfScriptedProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error {
	return nil
}
func (p *rfScriptedProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

func (p *rfScriptedProto) callModels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func TestGatewayResponseFormat_knownStructuredPassthrough(t *testing.T) {
	proto := &rfScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{
		Model:          "qwen-turbo",
		ResponseFormat: &domain.ResponseFormat{Type: "json_object"},
	})
	require.NoError(t, err)
	// qwen 族支持 json_object：response_format 原样透传。
	require.Equal(t, []string{"qwen-turbo:json_object"}, proto.callModels())
}

func TestGatewayResponseFormat_unsupportedClearedAndWarned(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	proto := &rfScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "hunyuan-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.New(core))
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{
		Model:          "hunyuan-turbo",
		ResponseFormat: &domain.ResponseFormat{Type: "json_object"},
	})
	require.NoError(t, err)
	// 未知/不支持模型：fail-closed，不盲透传（其严格端点 400 会中止 fallback 链）。
	require.Equal(t, []string{"hunyuan-turbo:none"}, proto.callModels())
	require.Equal(t, 1, logs.FilterMessage("llmgateway: response_format ignored for model without json_object support").Len())
}

func TestGatewayResponseFormat_nilNotInjected(t *testing.T) {
	proto := &rfScriptedProto{}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	// 调用方未请求结构化输出：即使模型支持也不注入（response_format 是 opt-in）。
	_, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{Model: "qwen-turbo"})
	require.NoError(t, err)
	require.Equal(t, []string{"qwen-turbo:none"}, proto.callModels())
}

func TestGatewayResponseFormat_fallbackCandidateCleared(t *testing.T) {
	// 主模型支持 json_object 但瞬态失败 → 降级到不支持候选：候选尝试必须
	// 清空 response_format，否则严格端点 400（永久错误）中止整条 fallback 链。
	proto := &rfScriptedProto{scripts: map[string]*modelScript{
		"qwen-turbo":    {completeErr: &statusErr{code: 500}},
		"hunyuan-turbo": {},
	}}
	gateway := reasoningGateway([]domain.Model{
		{ID: "m1", ProviderID: "p1", Name: "qwen-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m2", ProviderID: "p1", Name: "hunyuan-turbo", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, proto).WithLogger(zap.NewNop())
	ctx := reqctx.WithTenantID(context.Background(), "test-tenant")

	resp, err := gateway.Complete(ctx, &infrastructure.CompletionRequest{
		Model:          "qwen-turbo",
		ResponseFormat: &domain.ResponseFormat{Type: "json_object"},
	})
	require.NoError(t, err)
	require.Equal(t, "hunyuan-turbo", resp.ModelResolved)
	require.Equal(t, []string{"qwen-turbo:json_object", "qwen-turbo:json_object", "hunyuan-turbo:none"}, proto.callModels())
}
