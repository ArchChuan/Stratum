package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// newHealthRegistryWithClock 构造注入可推进时钟的 HealthRegistry，用于把模型
// 驱动到 unhealthy（5 次失败 → degraded，超 recovery 窗口再失败 → unhealthy）。
func newHealthRegistryWithClock(t *testing.T) (*infrastructure.HealthRegistry, *proberClock) {
	t.Helper()
	clock := &proberClock{cur: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)}
	return infrastructure.NewHealthRegistry(clock.Now), clock
}

// driveToUnhealthy 把模型驱动到 unhealthy 状态。
func driveToUnhealthy(t *testing.T, h *infrastructure.HealthRegistry, clock *proberClock, model string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		h.RecordFailure(model, errors.New("upstream"))
	}
	clock.Advance(40 * time.Second)
	h.RecordFailure(model, errors.New("still down"))
	require.Equal(t, infrastructure.ModelHealthUnhealthy, h.Get(model).Status)
}

// newChatRegistry 构造挂健康 registry 的 ModelRegistry（仅解析不发请求，
// baseURL 可为任意值）。proto 同时满足 chat/embed 协议。
func newChatRegistry(health *infrastructure.HealthRegistry, models []domain.Model, providers map[string]*domain.Provider) *infrastructure.ModelRegistry {
	proto := infrastructure.NewOpenAICompatProtocol(infrastructure.NewOpenAICompatClient(
		infrastructure.ProviderConfig{Name: "x"}, zap.NewNop()))
	return infrastructure.NewModelRegistry(
		&mockModelRepo{models: models},
		&mockProviderRepo{providers: providers},
		map[domain.ProviderKind]infrastructure.ChatProtocol{domain.ProviderOpenAICompat: proto},
		map[domain.ProviderKind]infrastructure.EmbedProtocol{domain.ProviderOpenAICompat: proto},
		time.Minute,
	).WithHealth(health)
}

func chatProvider() map[string]*domain.Provider {
	return map[string]*domain.Provider{
		"p1": {Name: "p", Enabled: true, Kind: domain.ProviderOpenAICompat, DefaultModel: "good-model"},
	}
}

// TestModelRegistry_ResolveExplicitUnhealthyFailsClosed 验证显式配置的模型
// unhealthy 时 fail-closed：配置层失效必须暴露给监控报警，禁止静默降级到
// 其他健康模型（代码内不写死兜底模型）。
func TestModelRegistry_ResolveExplicitUnhealthyFailsClosed(t *testing.T) {
	health, clock := newHealthRegistryWithClock(t)
	registry := newChatRegistry(health, []domain.Model{
		{Name: "bad-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "good-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, chatProvider())
	driveToUnhealthy(t, health, clock, "bad-model")

	_, _, err := registry.Resolve(context.Background(), "bad-model")
	require.Error(t, err)
	require.ErrorContains(t, err, infrastructure.ErrModelNotInCatalog.Error())
}

// TestModelRegistry_ResolveAllUnhealthyFailsClosed 验证 H1 链尾：全部候选
// unhealthy 时 fail-closed 报错，禁止返回熔断模型。
func TestModelRegistry_ResolveAllUnhealthyFailsClosed(t *testing.T) {
	health, clock := newHealthRegistryWithClock(t)
	// provider 默认模型同样指向熔断模型，确保 ②③④ 级全无可选健康候选
	providers := map[string]*domain.Provider{
		"p1": {Name: "p", Enabled: true, Kind: domain.ProviderOpenAICompat, DefaultModel: "bad-model"},
	}
	registry := newChatRegistry(health, []domain.Model{
		{Name: "bad-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, providers)
	driveToUnhealthy(t, health, clock, "bad-model")

	_, _, err := registry.Resolve(context.Background(), "bad-model")
	require.Error(t, err)
}

// TestModelRegistry_ResolveCacheHitButUnhealthyFailsClosed 验证 M1：TTL 缓存
// 命中但模型转 unhealthy 后不走缓存，显式请求 fail-closed（不再降级到其他
// 模型）。
func TestModelRegistry_ResolveCacheHitButUnhealthyFailsClosed(t *testing.T) {
	health, clock := newHealthRegistryWithClock(t)
	registry := newChatRegistry(health, []domain.Model{
		{Name: "primary", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "good-model", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, chatProvider())

	// 首次解析 primary 写入缓存
	cfg, _, err := registry.Resolve(context.Background(), "primary")
	require.NoError(t, err)
	require.Equal(t, "primary", cfg.Models[0])

	// primary 转 unhealthy：缓存命中但不可用 → 显式请求 fail-closed
	driveToUnhealthy(t, health, clock, "primary")
	_, _, err = registry.Resolve(context.Background(), "primary")
	require.Error(t, err)
	require.ErrorContains(t, err, infrastructure.ErrModelNotInCatalog.Error())
}

// TestModelRegistry_ResolveDefaultSkipsUnhealthyProviderDefault 验证 ② 级：
// provider.default_model 已 unhealthy 时跳过，落到 ③ recommended 健康模型。
func TestModelRegistry_ResolveDefaultSkipsUnhealthyProviderDefault(t *testing.T) {
	health, clock := newHealthRegistryWithClock(t)
	providers := map[string]*domain.Provider{
		"p1": {Name: "p", Enabled: true, Kind: domain.ProviderOpenAICompat, DefaultModel: "bad-default"},
	}
	registry := newChatRegistry(health, []domain.Model{
		{Name: "bad-default", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "rec-model", ProviderID: "p1", Recommended: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, providers)
	driveToUnhealthy(t, health, clock, "bad-default")

	// 空显式指定 → ②(default=bad-default,unhealthy→skip) → ③(rec-model)
	cfg, _, err := registry.Resolve(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "rec-model", cfg.Models[0])
}

// TestModelRegistry_FallbackCandidatesSkipUnhealthy 验证降级候选跳过已熔断
// 模型：unhealthy 候选不入 fallback 链。
func TestModelRegistry_FallbackCandidatesSkipUnhealthy(t *testing.T) {
	health, clock := newHealthRegistryWithClock(t)
	providers := map[string]*domain.Provider{
		"p1": {Name: "p", Enabled: true, Kind: domain.ProviderOpenAICompat, DefaultModel: "primary"},
	}
	registry := newChatRegistry(health, []domain.Model{
		{Name: "primary", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "bad-cand", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
		{Name: "good-cand", ProviderID: "p1", Capabilities: []domain.ModelCapability{domain.CapChat}},
	}, providers)
	driveToUnhealthy(t, health, clock, "bad-cand")

	cands, err := registry.ResolveFallbackCandidates(context.Background(), "primary")
	require.NoError(t, err)
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		names = append(names, c.Model)
	}
	require.NotContains(t, names, "bad-cand")
	require.Contains(t, names, "good-cand")
}
