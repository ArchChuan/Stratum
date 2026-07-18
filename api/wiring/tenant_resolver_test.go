package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
