package wiring

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testSettingsFn(settings []byte) llmgateway.TenantSettingsFn {
	return func(_ context.Context, _ string) ([]byte, error) {
		return settings, nil
	}
}

func TestTenantCapabilityResolverWorkerResolveReportsInfrastructureFailure(t *testing.T) {
	resolver := &tenantCapabilityResolver{
		registry: nil,
		gateway:  nil,
		logger:   zap.NewNop(),
	}

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "registry unavailable")
}

func TestTenantCapabilityResolverValidateTenantChatModelRejectsUnconfiguredAndUnknownModel(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("tenant-model-validator-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)

	settings := []byte(`{"llm_api_keys":{"qwen":"` + encrypted + `"}}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	require.NoError(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "qwen-plus"))
	require.ErrorIs(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "glm-4"),
		domain.ErrInvalidSystemAssistantModel)
}

func TestTenantCapabilityResolverValidateUnconfiguredRejectsFallback(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("tenant-model-validator-key")

	settings := []byte(`{}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	require.ErrorIs(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "qwen-plus"),
		domain.ErrAssistantModelUnavailable)
}

func TestTenantCapabilityResolverListTenantChatModelsIncludesOnlyConfiguredProviders(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("tenant-model-catalog-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)

	settings := []byte(`{"llm_api_keys":{"qwen":"` + encrypted + `"}}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Contains(t, models, "qwen-plus")
	require.NotContains(t, models, "glm-4")
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyWhenUnconfigured(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("unconfigured-catalog-key")
	settings := []byte(`{}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyForUnsupportedProviders(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("unsupported-model-catalog-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)

	settings := []byte(`{"llm_api_keys":{"stale":"` + encrypted + `"}}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestNewTenantCapabilityResolverPreservesNilDatabaseBehavior(t *testing.T) {
	resolver := newTenantCapabilityResolver(nil, nil, zap.NewNop()).(*tenantCapabilityResolver)

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "registry unavailable")
}

func TestTenantCapabilityResolverRejectsLoadInvalidatedWhileBlocked(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("fake-resolver-test-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "fake-key-a")
	require.NoError(t, err)

	settings := []byte(`{"llm_api_keys":{"qwen":"` + encrypted + `"}}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	// First resolve should succeed.
	_, err = resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.NoError(t, err)

	// Invalidate and resolve again — should re-populate from settings.
	registry.Invalidate("tenant-1")
	_, err = resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.NoError(t, err)
}

func TestTenantCapabilityResolverRejectsUnsupportedProvider(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("fake-resolver-test-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "fake-key-unsupported")
	require.NoError(t, err)

	settings := []byte(`{"llm_api_keys":{"unsupported":"` + encrypted + `"}}`)
	registry := llmgateway.NewModelRegistry(testSettingsFn(settings), aesKey, zap.NewNop())
	resolver := newTenantCapabilityResolver(registry, nil, zap.NewNop()).(*tenantCapabilityResolver)

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "no usable provider")
}
