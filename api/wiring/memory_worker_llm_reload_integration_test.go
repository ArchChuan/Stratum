package wiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMemoryWorkerReloadsTenantCredentialThroughSettingsPath(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_URL")
	required := os.Getenv("REQUIRE_MEMORY_E2E") == "1"
	if dsn == "" {
		if required {
			t.Fatal("TEST_POSTGRES_URL is required when REQUIRE_MEMORY_E2E=1")
		}
		t.Skip("TEST_POSTGRES_URL is not configured")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		handleWorkerReloadDependencyFailure(t, required, err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		handleWorkerReloadDependencyFailure(t, required, err)
	}
	require.NoError(t, pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()))

	const (
		fakeKeyA = "fake-memory-worker-key-a"
		fakeKeyB = "fake-memory-worker-key-b"
	)
	var callsMu sync.Mutex
	var credentialFingerprints []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fingerprint string
		switch r.Header.Get("Authorization") {
		case "Bearer " + fakeKeyA:
			fingerprint = "A"
		case "Bearer " + fakeKeyB:
			fingerprint = "B"
		default:
			http.Error(w, "unrecognized fake credential", http.StatusUnauthorized)
			return
		}
		callsMu.Lock()
		credentialFingerprints = append(credentialFingerprints, fingerprint)
		callsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "summary-" + fingerprint}}},
			"model":   "qwen-turbo",
		})
	}))
	defer server.Close()

	tenantID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO public.tenants (id, name, slug, settings) VALUES ($1, $2, $3, '{}'::jsonb)`, tenantID, "worker reload test", "worker-reload-"+tenantID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id=$1`, tenantID) })

	aesKey := pkgcrypto.DeriveAESKey("fake-memory-worker-test-key-material")

	// ModelRegistry reads tenant settings from DB and decrypts per-provider keys.
	registry := llmgateway.NewModelRegistry(nil, aesKey, zap.NewNop())
	gateway := llmgateway.NewGateway(registry)
	resolver := newTenantCapabilityResolver(registry, gateway, zap.NewNop()).(*tenantCapabilityResolver)

	_ = iamapp.NewTenantService(iampersistence.NewTenantRepo(pool), zap.NewNop(), aesKey, nil)

	processor := memworkers.NewResolvingLLMHistorySummarizer(tenantID, func(ctx context.Context, tenantID string) (memworkers.TenantLLMClient, error) {
		client, err := resolver.ResolveWorkerLLM(ctx, tenantID)
		if err != nil || client == nil {
			return nil, err
		}
		return memoryLLMAdapter{client: client}, nil
	})

	// No llm_api_keys configured — WarmTenant returns nothing, gateway has no provider.
	_, err = processor.SummarizeHistory(ctx, []string{"before configuration"})
	require.Error(t, err)
	require.Empty(t, credentialFingerprints)

	// Seed tenant settings with qwen key.
	keyDataA, _ := json.Marshal(map[string]any{"qwen": fakeKeyA, "base_urls": map[string]any{"qwen": server.URL}})
	_, err = pool.Exec(ctx, `UPDATE public.tenants SET settings=$1 WHERE id=$2`, string(keyDataA), tenantID)
	require.NoError(t, err)
	// Force registry re-read so it picks up the new key.
	registry.Invalidate(tenantID)

	first, err := processor.SummarizeHistory(ctx, []string{"first"})
	require.NoError(t, err)
	require.Equal(t, "summary-A", first)

	// Change key — registry must pick up the new value after invalidation.
	keyDataB, _ := json.Marshal(map[string]any{"qwen": fakeKeyB, "base_urls": map[string]any{"qwen": server.URL}})
	_, err = pool.Exec(ctx, `UPDATE public.tenants SET settings=$1 WHERE id=$2`, string(keyDataB), tenantID)
	require.NoError(t, err)
	registry.Invalidate(tenantID)

	second, err := processor.SummarizeHistory(ctx, []string{"second"})
	require.NoError(t, err)
	require.Equal(t, "summary-B", second)
	callsMu.Lock()
	require.Equal(t, []string{"A", "B"}, credentialFingerprints)
	callsMu.Unlock()

	_ = time.Now // keep time import
}

func handleWorkerReloadDependencyFailure(t *testing.T, required bool, err error) {
	t.Helper()
	if required {
		t.Fatalf("PostgreSQL unavailable while REQUIRE_MEMORY_E2E=1: %v", err)
	}
	t.Skipf("PostgreSQL unavailable: %v", err)
}
