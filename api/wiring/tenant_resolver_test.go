package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTenantCapabilityResolverWorkerResolveReportsInfrastructureFailure(t *testing.T) {
	resolver := &tenantCapabilityResolver{
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "database unavailable")
}

func TestTenantCapabilityResolverValidateTenantChatModelRejectsFallbackAndUnknownModel(t *testing.T) {
	missingSettings, err := json.Marshal(map[string]any{})
	require.NoError(t, err)
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: missingSettings}
		}),
		cache:    llmgateway.NewTenantGatewayCache(),
		fallback: llmgateway.NewGateway(),
		logger:   zap.NewNop(),
	}
	require.ErrorIs(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "qwen-plus"),
		domain.ErrAssistantModelUnavailable)

	aesKey := pkgcrypto.DeriveAESKey("tenant-model-validator-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)
	configured, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)
	resolver = &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: configured}
		}),
		aesKey: aesKey,
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}
	require.NoError(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "qwen-plus"))
	require.ErrorIs(t, resolver.ValidateTenantChatModel(context.Background(), "tenant-1", "glm-4"),
		domain.ErrInvalidSystemAssistantModel)
}

func TestTenantCapabilityResolverListTenantChatModelsIncludesOnlyConfiguredProviders(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("tenant-model-catalog-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)
	configured, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: configured}
		}),
		aesKey: aesKey,
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Contains(t, models, "qwen-plus")
	require.NotContains(t, models, "glm-4")
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyWhenUnconfigured(t *testing.T) {
	settings, err := json.Marshal(map[string]any{})
	require.NoError(t, err)
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: settings}
		}),
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyForUnsupportedProviders(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("unsupported-model-catalog-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"stale": encrypted}})
	require.NoError(t, err)
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: settings}
		}),
		aesKey: aesKey,
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}

	models, err := resolver.ListTenantChatModels(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestNewTenantCapabilityResolverPreservesNilDatabaseBehavior(t *testing.T) {
	resolver := newTenantCapabilityResolver(
		nil,
		[32]byte{},
		llmgateway.NewTenantGatewayCache(),
		nil,
		zap.NewNop(),
		"",
		"",
	).(*tenantCapabilityResolver)

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "database unavailable")
}

type tenantSettingsQueryFunc func(context.Context, string, ...any) pgx.Row

func (f tenantSettingsQueryFunc) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return f(ctx, query, args...)
}

type tenantSettingsRow struct {
	settings []byte
	read     chan<- struct{}
	release  <-chan struct{}
}

func (r tenantSettingsRow) Scan(dest ...any) error {
	if r.read != nil {
		close(r.read)
	}
	if r.release != nil {
		<-r.release
	}
	settings, ok := dest[0].(*[]byte)
	if !ok {
		return errors.New("unexpected scan destination")
	}
	*settings = append([]byte(nil), r.settings...)
	return nil
}

func TestTenantCapabilityResolverRejectsLoadInvalidatedWhileBlocked(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("fake-resolver-test-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "fake-key-a")
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}})
	require.NoError(t, err)
	read := make(chan struct{})
	release := make(chan struct{})
	cache := llmgateway.NewTenantGatewayCache()
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: settings, read: read, release: release}
		}),
		aesKey: aesKey,
		cache:  cache,
		logger: zap.NewNop(),
	}
	result := make(chan error, 1)
	go func() {
		_, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
		result <- err
	}()
	<-read
	cache.Invalidate("tenant-1")
	close(release)

	require.ErrorContains(t, <-result, "configuration changed during resolve")
	_, _, hit := cache.Get("tenant-1")
	require.False(t, hit)
}

func TestTenantCapabilityResolverRejectsUnsupportedProvider(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("fake-resolver-test-key-material")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "fake-key-unsupported")
	require.NoError(t, err)
	settings, err := json.Marshal(map[string]any{"llm_api_keys": map[string]any{"unsupported": encrypted}})
	require.NoError(t, err)
	resolver := &tenantCapabilityResolver{
		db: tenantSettingsQueryFunc(func(context.Context, string, ...any) pgx.Row {
			return tenantSettingsRow{settings: settings}
		}),
		aesKey: aesKey,
		cache:  llmgateway.NewTenantGatewayCache(),
		logger: zap.NewNop(),
	}

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "no supported provider configured")
}

func TestTenantCapabilityResolverDiagnosticModelStatus(t *testing.T) {
	aesKey := pkgcrypto.DeriveAESKey("diagnostic-model-key")
	encrypted, err := pkgcrypto.Encrypt(aesKey, "provider-key")
	require.NoError(t, err)
	tests := []struct {
		name       string
		settings   map[string]any
		configured bool
		wantErr    bool
	}{
		{name: "not configured", settings: map[string]any{}, configured: false},
		{name: "configured", settings: map[string]any{"llm_api_keys": map[string]any{"qwen": encrypted}}, configured: true},
		{name: "decrypt failure", settings: map[string]any{"llm_api_keys": map[string]any{"qwen": "not-ciphertext"}}, wantErr: true},
		{name: "unsupported", settings: map[string]any{"llm_api_keys": map[string]any{"other": encrypted}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, marshalErr := json.Marshal(tt.settings)
			require.NoError(t, marshalErr)
			resolver := &tenantCapabilityResolver{db: tenantSettingsQueryFunc(func(_ context.Context, _ string, args ...any) pgx.Row {
				require.Equal(t, "tenant-1", args[0])
				return tenantSettingsRow{settings: raw}
			}), aesKey: aesKey, logger: zap.NewNop()}
			status, diagnosticErr := resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
			require.Equal(t, tt.wantErr, diagnosticErr != nil)
			require.Equal(t, tt.configured, status.Configured)
		})
	}
}
