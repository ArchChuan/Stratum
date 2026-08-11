package infrastructure_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// statusErr 模拟带 HTTP 状态码的 provider 错误。
type statusErr struct {
	code int
	msg  string
}

func (e *statusErr) Error() string   { return e.msg }
func (e *statusErr) StatusCode() int { return e.code }

// modelScript 编排单个模型的 Complete/CompleteStream 行为。
type modelScript struct {
	// completeErr 每次调用恒返回该错误；completeSeq 逐次消费（nil 元素=
	// 成功），耗尽后成功，优先级高于 completeErr。
	completeErr          error
	completeSeq          []error
	streamErr            error
	streamFailAfterToken bool // 首 token 已流出后返回 streamErr
}

// scriptedProto 按模型名分派行为的 ChatProtocol 实现，并记录调用序列。
type scriptedProto struct {
	mu      sync.Mutex
	scripts map[string]*modelScript
	calls   []string
}

func newScriptedProto(scripts map[string]*modelScript) *scriptedProto {
	return &scriptedProto{scripts: scripts}
}

func (p *scriptedProto) record(model string) *modelScript {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, model)
	return p.scripts[model]
}

func (p *scriptedProto) callModels() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func (p *scriptedProto) Complete(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest) (*infrastructure.CompletionResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req.Model)
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
	return &infrastructure.CompletionResponse{Content: "ok-" + req.Model, Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}

func (p *scriptedProto) CompleteStream(ctx context.Context, cfg infrastructure.ProviderConfig, req *infrastructure.CompletionRequest, onToken func(string)) (*infrastructure.CompletionResponse, error) {
	s := p.record(req.Model)
	if s != nil && s.streamFailAfterToken {
		onToken("partial")
		return nil, s.streamErr
	}
	if s != nil && s.streamErr != nil {
		return nil, s.streamErr
	}
	onToken("hello")
	return &infrastructure.CompletionResponse{Content: "hello", Usage: infrastructure.TokenUsage{PromptTokens: 1, CompletionTokens: 1}}, nil
}

func (p *scriptedProto) Health(ctx context.Context, cfg infrastructure.ProviderConfig) error {
	return nil
}
func (p *scriptedProto) ListModels(ctx context.Context, cfg infrastructure.ProviderConfig) ([]infrastructure.DiscoveredModel, error) {
	return nil, nil
}

// fallbackFixture 构造单 provider（openai-compat）+ 多模型 registry 与 gateway。
type fallbackFixture struct {
	gateway *infrastructure.Gateway
	proto   *scriptedProto
}

func newFallbackFixture(t *testing.T, scripts map[string]*modelScript) *fallbackFixture {
	t.Helper()
	models := []domain.Model{
		{ID: "m-primary", ProviderID: "p1", Name: "primary", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-c1", ProviderID: "p1", Name: "cand-a", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-c2", ProviderID: "p1", Name: "cand-b", Enabled: true, Recommended: false,
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
	embedProtos := map[domain.ProviderKind]infrastructure.EmbedProtocol{}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, embedProtos, 5*time.Minute)
	gateway := infrastructure.NewGateway(reg, chatProtos, embedProtos)
	return &fallbackFixture{gateway: gateway, proto: proto}
}

func ctxWithTenant() context.Context {
	return reqctx.WithTenantID(context.Background(), "test-tenant")
}

func TestCompletePrimarySuccessFillsRouteInfo(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{"primary": {}})
	resp, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.NoError(t, err)
	require.Equal(t, "primary", resp.ModelResolved)
	require.Equal(t, []string{"primary"}, resp.ModelRoutedVia)
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

func TestCompletePrimaryTransientRetriesOnceBeforeFallback(t *testing.T) {
	rateLimit := &statusErr{code: 429, msg: "rate limited"}
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeErr: rateLimit},
	})
	// primary 每次调用都 429 → 第一次失败后立即重试仍失败 → 降级 cand-a 成功。
	resp, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.NoError(t, err)
	require.Equal(t, "cand-a", resp.ModelResolved)
	require.Equal(t, []string{"primary", "cand-a"}, resp.ModelRoutedVia)
	require.Equal(t, []string{"primary", "primary", "cand-a"}, f.proto.callModels())
}

func TestCompletePrimaryRetrySucceedsWithoutFallback(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeSeq: []error{&statusErr{code: 503, msg: "unavailable"}}},
	})
	// 主模型 503 一次 → 立即重试成功 → 不降级。
	resp, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.NoError(t, err)
	require.Equal(t, "primary", resp.ModelResolved)
	require.Equal(t, []string{"primary"}, resp.ModelRoutedVia)
	require.Equal(t, []string{"primary", "primary"}, f.proto.callModels())
}

func TestCompletePermanentErrorStopsChain(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeErr: &statusErr{code: 400, msg: "bad request"}},
	})
	_, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.Error(t, err)
	require.True(t, infrastructure.IsPermanent(err), "permanent error must be marked")
	// 永久错误不触发重试、不降级。
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

func TestCompleteExhaustedReturnsWrappedPermanentError(t *testing.T) {
	boom := &statusErr{code: 500, msg: "boom"}
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeErr: boom},
		"cand-a":  {completeErr: &statusErr{code: 502, msg: "bad gateway"}},
		"cand-b":  {completeErr: &statusErr{code: 503, msg: "unavailable"}},
	})
	_, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.Error(t, err)
	require.True(t, infrastructure.IsPermanent(err), "exhausted chain must be marked permanent")
	// 主模型 2 次 + 2 个候选各 1 次；候选上限 3 但只注册 2 个。
	require.Equal(t, []string{"primary", "primary", "cand-a", "cand-b"}, f.proto.callModels())
	// 包装错误保留单次尝试链（errors.Is 可命中）。
	require.True(t, errors.Is(err, boom))
}

func TestCompleteCanceledNeverTriggersFallback(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeErr: context.Canceled},
	})
	_, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "canceled must stay detectable")
	require.Equal(t, []string{"primary"}, f.proto.callModels(), "canceled must not retry or fallback")
}

func TestCompleteStreamFallbackBeforeFirstToken(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {streamErr: &statusErr{code: 429, msg: "rate limited"}},
	})
	resp, err := f.gateway.CompleteStream(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"}, func(tok string) {
		mu.Lock()
		tokens = append(tokens, tok)
		mu.Unlock()
	})
	require.NoError(t, err)
	require.Equal(t, "cand-a", resp.ModelResolved)
	require.Equal(t, []string{"primary", "primary", "cand-a"}, f.proto.callModels())
	// 首 token 前失败可降级：客户端只收到候选模型的 token，无重复输出。
	require.Equal(t, []string{"hello"}, tokens)
}

func TestCompleteStreamFailureAfterFirstTokenDoesNotFallback(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {streamErr: &statusErr{code: 500, msg: "boom"}, streamFailAfterToken: true},
	})
	_, err := f.gateway.CompleteStream(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"}, func(string) {})
	require.Error(t, err)
	require.True(t, infrastructure.IsPermanent(err), "mid-stream failure must be marked permanent")
	// 首 token 已流出：重试或降级都会向客户端重复输出，故一次调用即停。
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

func TestCompleteStreamTruncatedAfterFirstTokenPropagates(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {streamErr: domain.ErrStreamTruncated, streamFailAfterToken: true},
	})
	_, err := f.gateway.CompleteStream(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"}, func(string) {})
	require.Error(t, err)
	require.True(t, infrastructure.IsPermanent(err), "truncation after first token must be permanent")
	require.True(t, errors.Is(err, domain.ErrStreamTruncated), "truncation must stay detectable up the chain")
	// 截断发生在首 token 之后：不得重试或降级（避免重复输出），一次调用即停。
	require.Equal(t, []string{"primary"}, f.proto.callModels())
}

func TestCompleteStreamPrimarySuccess(t *testing.T) {
	f := newFallbackFixture(t, map[string]*modelScript{"primary": {}})
	resp, err := f.gateway.CompleteStream(ctxWithTenant(), &infrastructure.CompletionRequest{Model: "primary"}, func(string) {})
	require.NoError(t, err)
	require.Equal(t, "primary", resp.ModelResolved)
	require.Equal(t, []string{"primary"}, resp.ModelRoutedVia)
}

// TestResolveFallbackCandidatesOrderingAndCap 验证候选排序（同 provider 优先 →
// Recommended desc → name asc）、上限 3 与过滤（disabled provider / 不支持 chat）。
func TestResolveFallbackCandidatesOrderingAndCap(t *testing.T) {
	models := []domain.Model{
		{ID: "m-primary", ProviderID: "p1", Name: "primary", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-same-rec", ProviderID: "p1", Name: "same-rec", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-same-non", ProviderID: "p1", Name: "same-non", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-other-rec", ProviderID: "p2", Name: "other-rec", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-other-non", ProviderID: "p2", Name: "other-non", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-other-extra", ProviderID: "p2", Name: "other-extra", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-disabled", ProviderID: "p3", Name: "disabled-prov", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-embed", ProviderID: "p4", Name: "embed-only", Enabled: true,
			Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
	}
	modelRepo := &mockModelRepo{models: models}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{
		"p1": {ID: "p1", Name: "P1", Kind: domain.ProviderOpenAICompat, BaseURL: "https://p1", DefaultModel: "primary", Enabled: true},
		"p2": {ID: "p2", Name: "P2", Kind: domain.ProviderOpenAICompat, BaseURL: "https://p2", DefaultModel: "other-rec", Enabled: true},
		"p3": {ID: "p3", Name: "P3", Kind: domain.ProviderOpenAICompat, BaseURL: "https://p3", DefaultModel: "disabled-prov", Enabled: false},
		"p4": {ID: "p4", Name: "P4", Kind: domain.ProviderAnthropic, BaseURL: "https://p4", DefaultModel: "embed-only", Enabled: true},
	}}
	proto := newScriptedProto(nil)
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}, 5*time.Minute)

	cands, err := reg.ResolveFallbackCandidates(ctxWithTenant(), "test-tenant", "primary")
	require.NoError(t, err)
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		names = append(names, c.Model)
	}
	// 同 provider(p1) 优先 → Recommended desc → name asc；上限 3；
	// disabled provider(p3) 与无 chat 协议(p4) 被过滤。
	require.Equal(t, []string{"same-rec", "same-non", "other-rec"}, names)
	for _, c := range cands {
		require.NotNil(t, c.Protocol)
	}
}

// TestInvokeWithFallback_NoPrimaryRetrySkipsImmediateRetry 验证压缩路径语义：
// NoPrimaryRetry=true 时主模型瞬态失败不立即重试，直接降级候选。
func TestInvokeWithFallback_NoPrimaryRetrySkipsImmediateRetry(t *testing.T) {
	unavailable := &statusErr{code: 503, msg: "unavailable"}
	f := newFallbackFixture(t, map[string]*modelScript{
		"primary": {completeErr: unavailable},
	})
	resp, err := f.gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{
		Model: "primary", NoPrimaryRetry: true,
	})
	require.NoError(t, err)
	require.Equal(t, "cand-a", resp.ModelResolved)
	require.Equal(t, []string{"primary", "cand-a"}, resp.ModelRoutedVia)
	// 主模型只调用 1 次即降级；默认（false）行为仍是「主模型 + 立即重试 + 候选」。
	require.Equal(t, []string{"primary", "cand-a"}, f.proto.callModels())
}

// TestInvokeWithFallback_MaxCandidatesTruncates 验证候选链按 MaxCandidates 截断：
// 注册 3 个候选，MaxCandidates=2 时只尝试前 2 个。
func TestInvokeWithFallback_MaxCandidatesTruncates(t *testing.T) {
	models := []domain.Model{
		{ID: "m-primary", ProviderID: "p1", Name: "primary", Enabled: true, Recommended: true,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-c1", ProviderID: "p1", Name: "cand-a", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-c2", ProviderID: "p1", Name: "cand-b", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
		{ID: "m-c3", ProviderID: "p1", Name: "cand-c", Enabled: true, Recommended: false,
			Capabilities: []domain.ModelCapability{domain.CapChat}},
	}
	modelRepo := &mockModelRepo{models: models}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{
		"p1": {ID: "p1", Name: "Test Provider", Kind: domain.ProviderOpenAICompat,
			BaseURL: "https://api.test", APIKey: "sk-test", DefaultModel: "primary", Enabled: true},
	}}
	proto := newScriptedProto(map[string]*modelScript{
		"primary": {completeErr: &statusErr{code: 500, msg: "boom"}},
		"cand-a":  {completeErr: &statusErr{code: 500, msg: "boom"}},
		"cand-b":  {completeErr: &statusErr{code: 500, msg: "boom"}},
		"cand-c":  {completeErr: &statusErr{code: 500, msg: "boom"}},
	})
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos,
		map[domain.ProviderKind]infrastructure.EmbedProtocol{}, 5*time.Minute)
	gateway := infrastructure.NewGateway(reg, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{})

	_, err := gateway.Complete(ctxWithTenant(), &infrastructure.CompletionRequest{
		Model: "primary", MaxCandidates: 2,
	})
	require.Error(t, err)
	require.True(t, infrastructure.IsPermanent(err), "exhausted chain must be marked permanent")
	// MaxCandidates=2：候选截断为前 2 个（同 provider → name asc），cand-c 不参与。
	require.Equal(t, []string{"primary", "primary", "cand-a", "cand-b"}, proto.callModels())
}

func TestResolveFallbackCandidatesPrimaryMissingFails(t *testing.T) {
	modelRepo := &mockModelRepo{models: []domain.Model{}}
	providerRepo := &mockProviderRepo{providers: map[string]*domain.Provider{}}
	proto := newScriptedProto(nil)
	chatProtos := map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto}
	reg := infrastructure.NewModelRegistry(modelRepo, providerRepo, chatProtos, map[domain.ProviderKind]infrastructure.EmbedProtocol{}, 5*time.Minute)
	_, err := reg.ResolveFallbackCandidates(ctxWithTenant(), "test-tenant", "ghost")
	require.Error(t, err)
	require.Contains(t, fmt.Sprintf("%v", err), "not found")
}
