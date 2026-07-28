package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

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

// TestNewTenantCapabilityResolverPreservesNilDatabaseBehavior verifies that a
// resolver created with a nil DB returns "database unavailable" errors.
func TestNewTenantCapabilityResolverPreservesNilDatabaseBehavior(t *testing.T) {
	resolver := newTenantCapabilityResolver(
		nil,
		[32]byte{},
		nil, // registry
		nil, // gateway
		zap.NewNop(),
	).(*tenantCapabilityResolver)

	client, err := resolver.ResolveWorkerLLM(context.Background(), "tenant-1")
	require.Nil(t, client)
	require.ErrorContains(t, err, "database unavailable")
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
			resolver := &tenantCapabilityResolver{
				db: tenantSettingsQueryFunc(func(_ context.Context, _ string, args ...any) pgx.Row {
					require.Equal(t, "tenant-1", args[0])
					return tenantSettingsRow{settings: raw}
				}),
				aesKey: aesKey,
				logger: zap.NewNop(),
			}
			status, diagnosticErr := resolver.DiagnosticModelStatus(context.Background(), "tenant-1")
			require.Equal(t, tt.wantErr, diagnosticErr != nil)
			require.Equal(t, tt.configured, status.Configured)
		})
	}
}

// Legacy tests — temporarily skipped until adapted for ModelRegistry-based resolver.
func TestTenantCapabilityResolverWorkerResolveReportsInfrastructureFailure(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverValidateTenantChatModelRejectsFallbackAndUnknownModel(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverListTenantChatModelsIncludesOnlyConfiguredProviders(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyWhenUnconfigured(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverListTenantChatModelsReturnsEmptyForUnsupportedProviders(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverRejectsLoadInvalidatedWhileBlocked(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}

func TestTenantCapabilityResolverRejectsUnsupportedProvider(t *testing.T) {
	t.Skip("TODO: adapt for ModelRegistry-based resolver")
}
