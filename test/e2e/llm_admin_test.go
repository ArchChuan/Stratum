package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/middleware"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockProviderRuntime stubs provider model discovery for E2E tests.
type mockProviderRuntime struct {
	listModels func(ctx context.Context, provider domain.Provider) ([]port.DiscoveredModel, error)
	health     func(ctx context.Context, provider domain.Provider) error
}

func (m *mockProviderRuntime) ListModels(ctx context.Context, provider domain.Provider) ([]port.DiscoveredModel, error) {
	if m.listModels != nil {
		return m.listModels(ctx, provider)
	}
	return []port.DiscoveredModel{{Name: "mock-model-1"}, {Name: "mock-model-2"}}, nil
}

func (m *mockProviderRuntime) Health(ctx context.Context, provider domain.Provider) error {
	if m.health != nil {
		return m.health(ctx, provider)
	}
	return nil
}

// llmAdminTestEnv holds the test router and dependencies.
type llmAdminTestEnv struct {
	PGPool       *pgxpool.Pool
	Router       *gin.Engine
	ProviderRepo port.ProviderRepository
	ModelRepo    port.ModelRepository
	ProviderSvc  *llmapp.ProviderService
	ModelMgmtSvc *llmapp.ModelMgmtService
	TenantID     string
	UserID       string
}

func llmAdminPostgresURL(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_POSTGRES_URL"); dsn != "" {
		return dsn
	}
	if os.Getenv("CI") != "" {
		t.Skip("LLM admin E2E requires TEST_POSTGRES_URL (skipped in CI without explicit env)")
	}
	return "postgres://stratum:stratum@localhost:5432/stratum_test?sslmode=disable"
}

// provisionPublicCatalog 幂等创建 public.providers/models 平台全局目录表。
// 测试直连业务库时不会自动应用 035 migration（test job 无 PG、本地库也不跑迁移），
// 此处按 035 up DDL 对齐建表，保证 provider/model repo 直连 public schema 有表可用。
// 若 035 表结构变更，需同步此处。
func provisionPublicCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS public.providers (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL UNIQUE,
			kind          TEXT NOT NULL,
			base_url      TEXT NOT NULL DEFAULT '',
			api_key       TEXT NOT NULL DEFAULT '',
			default_model TEXT NOT NULL DEFAULT '',
			enabled       BOOLEAN NOT NULL DEFAULT true,
			extra_headers JSONB NOT NULL DEFAULT '{}',
			default_sampling JSONB NOT NULL DEFAULT '{}',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS extra_headers JSONB NOT NULL DEFAULT '{}'`,
		`ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS default_sampling JSONB NOT NULL DEFAULT '{}'`,
		`CREATE TABLE IF NOT EXISTS public.models (
			id                TEXT PRIMARY KEY,
			provider_id       TEXT NOT NULL REFERENCES public.providers(id) ON DELETE CASCADE,
			name              TEXT NOT NULL,
			display_name      TEXT NOT NULL DEFAULT '',
			capabilities      TEXT[] NOT NULL DEFAULT '{}',
			context_window    INT NOT NULL DEFAULT 0,
			max_tokens        INT NOT NULL DEFAULT 0,
			sampling_params   JSONB NOT NULL DEFAULT '{}',
			max_temperature   DOUBLE PRECISION,
			input_price       DOUBLE PRECISION NOT NULL DEFAULT 0,
			output_price      DOUBLE PRECISION NOT NULL DEFAULT 0,
			recommended       BOOLEAN NOT NULL DEFAULT false,
			enabled           BOOLEAN NOT NULL DEFAULT true,
			provider_managed  BOOLEAN NOT NULL DEFAULT false,
			default_embedding BOOLEAN NOT NULL DEFAULT false,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(provider_id, name)
		)`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS sampling_params JSONB NOT NULL DEFAULT '{}'`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS max_temperature DOUBLE PRECISION`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS operator_context_window INT`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS operator_max_tokens INT`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS default_output_tokens INT`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS context_window_source TEXT NOT NULL DEFAULT 'legacy_unknown'`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS max_tokens_source TEXT NOT NULL DEFAULT 'legacy_unknown'`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS context_window_observed_at TIMESTAMPTZ`,
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS max_tokens_observed_at TIMESTAMPTZ`,
		// 040: 平台级显式降级候选链 —— 与 pkg/migration/sql/040_model_fallback_candidates.up.sql 对齐。
		`ALTER TABLE public.models ADD COLUMN IF NOT EXISTS fallback_candidates TEXT[] NOT NULL DEFAULT '{}'`,
		`CREATE TABLE IF NOT EXISTS public.platform_resource_change_audits (
			id                TEXT PRIMARY KEY,
			scope             TEXT NOT NULL DEFAULT 'platform',
			resource_kind     TEXT NOT NULL,
			resource_id       TEXT NOT NULL,
			operation         TEXT NOT NULL,
			actor_id          TEXT NOT NULL DEFAULT '',
			actor_tenant_id   TEXT,
			actor_type        TEXT NOT NULL DEFAULT 'user',
			source            TEXT NOT NULL DEFAULT 'api',
			proposal_id       TEXT NOT NULL DEFAULT '',
			before_projection JSONB NOT NULL DEFAULT '{}',
			after_projection  JSONB NOT NULL DEFAULT '{}',
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_models_provider ON public.models(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_models_enabled ON public.models(enabled)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
			ON public.models ((true))
			WHERE default_embedding AND 'embedding' = ANY(capabilities)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("provision public catalog: %v", err)
		}
	}
}

func setupLLMAdminTestEnv(t *testing.T) *llmAdminTestEnv {
	t.Helper()
	ctx := context.Background()

	dsn := llmAdminPostgresURL(t)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "connect to PostgreSQL")
	require.NoError(t, pool.Ping(ctx), "PostgreSQL must be reachable")

	// Provision public + tenant schema
	require.NoError(t, pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	provisionPublicCatalog(t, pool)
	// 测试库专用：清空全局目录表，保证 create 不撞 providers.name UNIQUE、
	// List 断言不被历史残留污染（语义等价旧 tenant 方案的每测试 DROP SCHEMA）。
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE public.providers CASCADE`); err != nil {
		require.NoError(t, err, "truncate public catalog")
	}
	tenantID := uuid.NewString()
	require.NoError(t, pgstorage.ProvisionTenantSchema(ctx, pool, tenantID))

	userID := uuid.NewString()

	// Build real repos
	modelRepo := llmgateway.NewPgModelRepo(pool)
	providerRepo := llmgateway.NewPgProviderRepo(pool, [32]byte{}, zap.NewNop(), observability.NoopMetrics{})

	// Build services with mock runtime
	runtime := &mockProviderRuntime{}
	providerSvc := llmapp.NewProviderService(providerRepo, modelRepo, runtime)
	modelMgmtSvc := llmapp.NewModelMgmtService(modelRepo)

	// Build handlers
	providerH := newTestProviderHandler(providerSvc)
	modelMgmtH := newTestModelMgmtHandler(modelMgmtSvc)

	// Set up gin router with tenant-injecting middleware (same pattern as
	// system_assistant_test.go)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Set("auth.tenant_id", tenantID)
		c.Set("auth.sub", userID)
		c.Set("auth.role", "admin")
		c.Next()
	}, middleware.InjectTenantContext())

	router.GET("/admin/providers", providerH.List)
	router.POST("/admin/providers", providerH.Create)
	router.PUT("/admin/providers/:id", providerH.Update)
	router.DELETE("/admin/providers/:id", providerH.Delete)
	router.POST("/admin/providers/:id/discover", providerH.Discover)

	router.GET("/admin/models", modelMgmtH.List)
	router.GET("/admin/models/:id", modelMgmtH.Get)
	router.PUT("/admin/models/:id", modelMgmtH.Update)
	router.PATCH("/admin/models/:id/toggle", modelMgmtH.Toggle)
	router.DELETE("/admin/models/:id", modelMgmtH.Delete)

	t.Cleanup(func() {
		// Only drop tenant schema — public schema is shared across tests
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID))
		pool.Close()
	})

	return &llmAdminTestEnv{
		PGPool:       pool,
		Router:       router,
		ProviderRepo: providerRepo,
		ModelRepo:    modelRepo,
		ProviderSvc:  providerSvc,
		ModelMgmtSvc: modelMgmtSvc,
		TenantID:     tenantID,
		UserID:       userID,
	}
}

func (e *llmAdminTestEnv) request(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.Router.ServeHTTP(rec, req)
	return rec
}

// unMarshalBody decodes a JSON response body into the given value.
func unMarshalBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), v))
}

// =============================================================================
// Provider CRUD E2E
// =============================================================================

func TestProviderCRUDLifecycle(t *testing.T) {
	env := setupLLMAdminTestEnv(t)

	// ── Create ──────────────────────────────────────────────────────────────
	createBody := `{"name":"TestVendor","kind":"openai_compat","baseUrl":"https://api.test.example.com","apiKey":"sk-test-key"}`
	rec := env.request(http.MethodPost, "/admin/providers", createBody)
	require.Equal(t, http.StatusCreated, rec.Code, "create should return 201")

	var created domain.Provider
	unMarshalBody(t, rec, &created)
	require.NotEmpty(t, created.ID, "created provider must have an ID")
	require.Equal(t, "TestVendor", created.Name)
	require.Equal(t, domain.ProviderOpenAICompat, created.Kind)
	require.Equal(t, "https://api.test.example.com", created.BaseURL)
	require.Empty(t, created.APIKey, "apiKey must never be serialized (write-only)")
	require.True(t, created.Enabled, "new providers default to enabled")
	require.NotZero(t, created.CreatedAt)
	require.NotZero(t, created.UpdatedAt)

	// Verify camelCase JSON keys
	require.Contains(t, rec.Body.String(), `"baseUrl"`)
	require.Contains(t, rec.Body.String(), `"createdAt"`)
	require.Contains(t, rec.Body.String(), `"updatedAt"`)
	require.Contains(t, rec.Body.String(), `"defaultModel"`)
	require.NotContains(t, rec.Body.String(), `"apiKey"`)
	require.NotContains(t, rec.Body.String(), `"api_key"`)
	require.NotContains(t, rec.Body.String(), `"TenantID"`)
	require.NotContains(t, rec.Body.String(), `"BaseURL"`)
	require.NotContains(t, rec.Body.String(), `"CreatedAt"`)
	require.NotContains(t, rec.Body.String(), `"UpdatedAt"`)

	// ── List ────────────────────────────────────────────────────────────────
	rec = env.request(http.MethodGet, "/admin/providers", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var listResp struct {
		Providers []domain.Provider `json:"providers"`
	}
	unMarshalBody(t, rec, &listResp)
	require.GreaterOrEqual(t, len(listResp.Providers), 1, "list must contain created provider")

	found := false
	for _, p := range listResp.Providers {
		if p.ID == created.ID {
			found = true
			require.Equal(t, "TestVendor", p.Name)
			require.Empty(t, p.APIKey, "list must not leak apiKey")
			break
		}
	}
	require.True(t, found, "created provider must appear in list")

	// Verify list response uses camelCase
	require.Contains(t, rec.Body.String(), `"providers"`)
	require.Contains(t, rec.Body.String(), `"baseUrl"`)
	require.Contains(t, rec.Body.String(), `"createdAt"`)

	// ── Update ──────────────────────────────────────────────────────────────
	updateBody := `{"name":"UpdatedVendor","kind":"openai_compat","baseUrl":"https://api.updated.example.com","apiKey":"","defaultModel":"mock-model-1"}`
	rec = env.request(http.MethodPut, "/admin/providers/"+created.ID, updateBody)
	require.Equal(t, http.StatusOK, rec.Code, "update should return 200")

	var updated domain.Provider
	unMarshalBody(t, rec, &updated)
	require.Equal(t, "UpdatedVendor", updated.Name)
	require.Equal(t, "https://api.updated.example.com", updated.BaseURL)
	require.Equal(t, "mock-model-1", updated.DefaultModel)
	require.Empty(t, updated.APIKey, "apiKey must remain hidden after update")

	// Verify update response uses camelCase
	require.Contains(t, rec.Body.String(), `"baseUrl"`)
	require.Contains(t, rec.Body.String(), `"defaultModel"`)

	// ── Delete ──────────────────────────────────────────────────────────────
	rec = env.request(http.MethodDelete, "/admin/providers/"+created.ID, "")
	require.Equal(t, http.StatusOK, rec.Code, "delete should return 200")

	// List again — provider should be gone
	rec = env.request(http.MethodGet, "/admin/providers", "")
	unMarshalBody(t, rec, &listResp)
	for _, p := range listResp.Providers {
		require.NotEqual(t, created.ID, p.ID, "deleted provider must not appear")
	}
}

// TestProviderCreateAndDiscover verifies the create-then-discover flow returns
// valid models.
func TestProviderCreateAndDiscover(t *testing.T) {
	env := setupLLMAdminTestEnv(t)

	// Create a provider
	createBody := `{"name":"DiscoverVendor","kind":"openai_compat","baseUrl":"https://api.discover.example.com","apiKey":"sk-discover"}`
	rec := env.request(http.MethodPost, "/admin/providers", createBody)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created domain.Provider
	unMarshalBody(t, rec, &created)

	// Trigger model discovery (uses mock runtime)
	rec = env.request(http.MethodPost, "/admin/providers/"+created.ID+"/discover", "")
	require.Equal(t, http.StatusOK, rec.Code, "discover should return 200")

	var discoverResp struct {
		Models []domain.Model `json:"models"`
		Count  int            `json:"count"`
	}
	unMarshalBody(t, rec, &discoverResp)
	require.Len(t, discoverResp.Models, 2, "mock runtime returns 2 models")
	require.Equal(t, 2, discoverResp.Count)
	for _, m := range discoverResp.Models {
		require.True(t, m.ProviderManaged, "discovered models are provider-managed")
		require.Equal(t, created.ID, m.ProviderID)
		require.NotEmpty(t, m.Name)
		// Verify camelCase JSON keys in model response
		rawBody := rec.Body.String()
		require.Contains(t, rawBody, `"providerManaged"`)
		require.Contains(t, rawBody, `"providerId"`)
	}
}

// =============================================================================
// Model CRUD E2E
// =============================================================================

func TestModelCRUDLifecycle(t *testing.T) {
	env := setupLLMAdminTestEnv(t)

	// ── Setup: create provider + discover models ────────────────────────────
	createBody := `{"name":"ModelTestVendor","kind":"openai_compat","baseUrl":"https://api.model-test.example.com","apiKey":"sk-model-test"}`
	rec := env.request(http.MethodPost, "/admin/providers", createBody)
	require.Equal(t, http.StatusCreated, rec.Code)
	var provider domain.Provider
	unMarshalBody(t, rec, &provider)

	rec = env.request(http.MethodPost, "/admin/providers/"+provider.ID+"/discover", "")
	require.Equal(t, http.StatusOK, rec.Code)

	// ── List models ─────────────────────────────────────────────────────────
	rec = env.request(http.MethodGet, "/admin/models", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var listResp struct {
		Models []domain.Model `json:"models"`
	}
	unMarshalBody(t, rec, &listResp)
	require.NotEmpty(t, listResp.Models, "discover should produce models")

	model := listResp.Models[0]

	// Verify camelCase JSON keys in model list
	rawBody := rec.Body.String()
	require.Contains(t, rawBody, `"displayName"`)
	require.Contains(t, rawBody, `"providerId"`)
	require.Contains(t, rawBody, `"providerManaged"`)
	require.Contains(t, rawBody, `"contextWindow"`)
	require.Contains(t, rawBody, `"maxTokens"`)
	require.Contains(t, rawBody, `"inputPrice"`)
	require.Contains(t, rawBody, `"outputPrice"`)
	require.Contains(t, rawBody, `"createdAt"`)
	require.Contains(t, rawBody, `"updatedAt"`)
	require.NotContains(t, rawBody, `"DisplayName"`)
	require.NotContains(t, rawBody, `"ProviderID"`)
	require.NotContains(t, rawBody, `"ContextWindow"`)
	require.NotContains(t, rawBody, `"MaxTokens"`)
	require.NotContains(t, rawBody, `"CreatedAt"`)

	// ── Get single model ────────────────────────────────────────────────────
	rec = env.request(http.MethodGet, "/admin/models/"+model.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var single domain.Model
	unMarshalBody(t, rec, &single)
	require.Equal(t, model.ID, single.ID)
	require.Equal(t, model.Name, single.Name)

	// Verify single model response uses camelCase
	require.Contains(t, rec.Body.String(), `"displayName"`)
	require.Contains(t, rec.Body.String(), `"providerManaged"`)

	// ── Update model ────────────────────────────────────────────────────────
	updateJSON := fmt.Sprintf(
		`{"displayName":"Custom Name","capabilities":["chat","reasoning"],"contextWindow":%d,"maxTokens":%d,"inputPrice":0.01,"outputPrice":0.03,"recommended":true}`,
		model.ContextWindow, model.MaxTokens,
	)
	rec = env.request(http.MethodPut, "/admin/models/"+model.ID, updateJSON)
	require.Equal(t, http.StatusOK, rec.Code, "update model should return 200")
	var updated domain.Model
	unMarshalBody(t, rec, &updated)
	require.Equal(t, "Custom Name", updated.DisplayName)
	require.Equal(t, model.ContextWindow, updated.ContextWindow)
	require.Equal(t, model.MaxTokens, updated.MaxTokens)
	require.Equal(t, 0.01, updated.InputPrice)
	require.Equal(t, 0.03, updated.OutputPrice)
	require.True(t, updated.Recommended)
	require.Len(t, updated.Capabilities, 2)

	// ── Toggle model ────────────────────────────────────────────────────────
	rec = env.request(http.MethodPatch, "/admin/models/"+model.ID+"/toggle", `{"enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code, "toggle should return 200")

	// Verify toggle took effect
	rec = env.request(http.MethodGet, "/admin/models/"+model.ID, "")
	unMarshalBody(t, rec, &single)
	require.False(t, single.Enabled, "model should be disabled after toggle")

	// Re-enable
	rec = env.request(http.MethodPatch, "/admin/models/"+model.ID+"/toggle", `{"enabled":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	rec = env.request(http.MethodGet, "/admin/models/"+model.ID, "")
	unMarshalBody(t, rec, &single)
	require.True(t, single.Enabled, "model should be re-enabled")

	// ── Delete provider-managed model is allowed ────────────────────────────
	rec = env.request(http.MethodDelete, "/admin/models/"+model.ID, "")
	require.Equal(t, http.StatusOK, rec.Code,
		"provider-managed models can be deleted directly (re-discovered on next sync)")
}

// =============================================================================
// JSON serialization guard — the bug that caused the original incident
// =============================================================================

func TestJSONSerializationCamelCaseKeys(t *testing.T) {
	// This test encodes domain structs directly and asserts the presence of
	// json tags — it guards against regression of the original PascalCase bug.

	t.Run("Provider JSON keys", func(t *testing.T) {
		provider := domain.Provider{
			ID:           "prov-1",
			Name:         "TestName",
			Kind:         domain.ProviderOpenAICompat,
			BaseURL:      "https://api.example.com",
			APIKey:       "secret",
			DefaultModel: "gpt-4",
			Enabled:      true,
		}
		b, err := json.Marshal(provider)
		require.NoError(t, err)
		raw := string(b)

		require.Contains(t, raw, `"id":"prov-1"`)
		require.Contains(t, raw, `"name":"TestName"`)
		require.Contains(t, raw, `"kind":"openai_compat"`)
		require.Contains(t, raw, `"baseUrl":"https://api.example.com"`)
		require.Contains(t, raw, `"defaultModel":"gpt-4"`)
		require.Contains(t, raw, `"enabled":true`)

		// Write-only fields must not leak
		require.NotContains(t, raw, `"apiKey"`)
		require.NotContains(t, raw, `"secret"`)
		require.NotContains(t, raw, `"tenantId"`)
		require.NotContains(t, raw, `"TenantID"`)

		// PascalCase keys must not appear
		require.NotContains(t, raw, `"ID"`)
		require.NotContains(t, raw, `"Name"`)
		require.NotContains(t, raw, `"Kind"`)
		require.NotContains(t, raw, `"BaseURL"`)
		require.NotContains(t, raw, `"DefaultModel"`)
		require.NotContains(t, raw, `"Enabled"`)
	})

	t.Run("Model JSON keys", func(t *testing.T) {
		model := domain.Model{
			ID:              "model-1",
			ProviderID:      "prov-1",
			Name:            "gpt-4",
			DisplayName:     "GPT-4",
			Capabilities:    []domain.ModelCapability{domain.CapChat, domain.CapReasoning},
			ContextWindow:   128000,
			MaxTokens:       4096,
			InputPrice:      0.01,
			OutputPrice:     0.03,
			Recommended:     true,
			Enabled:         true,
			ProviderManaged: true,
		}
		b, err := json.Marshal(model)
		require.NoError(t, err)
		raw := string(b)

		require.Contains(t, raw, `"id":"model-1"`)
		require.Contains(t, raw, `"providerId":"prov-1"`)
		require.Contains(t, raw, `"name":"gpt-4"`)
		require.Contains(t, raw, `"displayName":"GPT-4"`)
		require.Contains(t, raw, `"contextWindow":128000`)
		require.Contains(t, raw, `"maxTokens":4096`)
		require.Contains(t, raw, `"inputPrice":0.01`)
		require.Contains(t, raw, `"outputPrice":0.03`)
		require.Contains(t, raw, `"recommended":true`)
		require.Contains(t, raw, `"enabled":true`)
		require.Contains(t, raw, `"providerManaged":true`)

		// PascalCase keys must not appear
		require.NotContains(t, raw, `"ID"`)
		require.NotContains(t, raw, `"TenantID"`)
		require.NotContains(t, raw, `"ProviderID"`)
		require.NotContains(t, raw, `"DisplayName"`)
		require.NotContains(t, raw, `"ContextWindow"`)
		require.NotContains(t, raw, `"MaxTokens"`)
		require.NotContains(t, raw, `"InputPrice"`)
		require.NotContains(t, raw, `"OutputPrice"`)
		require.NotContains(t, raw, `"ProviderManaged"`)
	})
}

// =============================================================================
// Global catalog: providers/models 提升为 public 平台全局目录
// =============================================================================

// TestLLMAdminGlobalCatalog 验证重构后的全局目录语义：providers 不再按租户
// schema 隔离（035 迁移），List 返回全部、任意 ID 可跨调用方 Get、
// 模型按 provider 全局解析。
func TestLLMAdminGlobalCatalog(t *testing.T) {
	ctx := context.Background()

	dsn := llmAdminPostgresURL(t)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	require.NoError(t, pgstorage.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	provisionPublicCatalog(t, pool)
	// 测试库专用：清空全局目录表，保证 List 断言精确（不依赖前序测试清理）。
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE public.providers CASCADE`); err != nil {
		require.NoError(t, err, "truncate public catalog")
	}

	// Build repos — TestLLMAdminGlobalCatalog tests the repo layer directly
	providerRepo := llmgateway.NewPgProviderRepo(pool, [32]byte{}, zap.NewNop(), observability.NoopMetrics{})
	modelRepo := llmgateway.NewPgModelRepo(pool)

	provA := &domain.Provider{
		ID: uuid.NewString(), Name: "Global-Provider-A",
		Kind: domain.ProviderOpenAICompat, BaseURL: "https://api.a.example.com",
		APIKey: "sk-tenant-a", Enabled: true,
	}
	provB := &domain.Provider{
		ID: uuid.NewString(), Name: "Global-Provider-B",
		Kind: domain.ProviderOpenAICompat, BaseURL: "https://api.b.example.com",
		APIKey: "sk-tenant-b", Enabled: true,
	}
	require.NoError(t, providerRepo.Create(ctx, provA))
	require.NoError(t, providerRepo.Create(ctx, provB))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.models WHERE provider_id IN ($1,$2)`, provA.ID, provB.ID)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.providers WHERE id IN ($1,$2)`, provA.ID, provB.ID)
		pool.Close()
	})

	// 全局目录：List 返回全部 provider，无租户过滤
	all, err := providerRepo.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// 全局目录：任意 provider ID 可直接 Get，不依赖调用方 tenant
	got, err := providerRepo.Get(ctx, provB.ID)
	require.NoError(t, err)
	require.Equal(t, "Global-Provider-B", got.Name)

	// 全局目录：模型按 provider 全局解析
	m := &domain.Model{
		ID: uuid.NewString(), ProviderID: provA.ID, Name: "global-model",
		Capabilities: []domain.ModelCapability{domain.CapChat}, Enabled: true,
	}
	require.NoError(t, modelRepo.Create(ctx, m))
	listed, err := modelRepo.List(ctx, port.ModelFilter{ProviderID: provA.ID})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "global-model", listed[0].Name)
}

// =============================================================================
// Integration with actual handler constructors (bridge to handler pkg)
// =============================================================================

// newTestProviderHandler builds a test-safe ProviderHandler.
// NOTE: this re-exports the handler package's constructor pattern to avoid
// importing the handler package directly from e2e, which creates circular deps.
// In production, wiring uses handler.NewProviderHandler — the test duplicates
// the minimal logic inline as an intentional copy.
func newTestProviderHandler(svc *llmapp.ProviderService) *providerHandlerProxy {
	return &providerHandlerProxy{svc: svc}
}

func newTestModelMgmtHandler(svc *llmapp.ModelMgmtService) *modelMgmtHandlerProxy {
	return &modelMgmtHandlerProxy{svc: svc}
}

// =============================================================================
// Proxy handlers — thin wrappers matching the real handler signatures
// but living in the e2e package to avoid circular imports.
// =============================================================================

type providerHandlerProxy struct {
	svc *llmapp.ProviderService
}

func (h *providerHandlerProxy) List(c *gin.Context) {
	// Replicates handler.ProviderHandler.List
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	providers, err := h.svc.List(c.Request.Context(), tid)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (h *providerHandlerProxy) Create(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	actorID, _ := userIDFromGin(c)
	var input llmapp.CreateProviderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	provider, err := h.svc.Create(c.Request.Context(), tid, actorID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, provider)
}

func (h *providerHandlerProxy) Update(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	var input llmapp.UpdateProviderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	input.ID = c.Param("id")
	actorID, _ := userIDFromGin(c)
	provider, err := h.svc.Update(c.Request.Context(), tid, actorID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, provider)
}

func (h *providerHandlerProxy) Delete(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	if err := h.svc.Delete(c.Request.Context(), tid, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

func (h *providerHandlerProxy) Discover(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	models, err := h.svc.DiscoverModels(c.Request.Context(), tid, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "count": len(models)})
}

type modelMgmtHandlerProxy struct {
	svc *llmapp.ModelMgmtService
}

func (h *modelMgmtHandlerProxy) List(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	models, err := h.svc.List(c.Request.Context(), tid, port.ModelFilter{})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (h *modelMgmtHandlerProxy) Get(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	m, err := h.svc.Get(c.Request.Context(), tid, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *modelMgmtHandlerProxy) Update(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	var input llmapp.UpdateModelInput
	if err := c.ShouldBindJSON(&input); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	input.ID = c.Param("id")
	actorID, _ := userIDFromGin(c)
	m, err := h.svc.Update(c.Request.Context(), tid, actorID, input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, m)
}

func (h *modelMgmtHandlerProxy) Toggle(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if err := h.svc.Toggle(c.Request.Context(), tid, c.Param("id"), req.Enabled); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已更新"})
}

func (h *modelMgmtHandlerProxy) Delete(c *gin.Context) {
	tid, ok := tenantIDFromGin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "tenant context required"})
		return
	}
	if err := h.svc.Delete(c.Request.Context(), tid, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

// userIDFromGin mirrors handler.userIDFromCtx but operates on gin.Context
// directly (the handler version reads from request context after injection).
func userIDFromGin(c *gin.Context) (string, bool) {
	uid, ok := c.Get("auth.sub")
	if !ok {
		return "", false
	}
	s, ok := uid.(string)
	if s == "" {
		return "", false
	}
	return s, ok
}

// tenantIDFromGin mirrors handler.tenantIDFromCtx but operates on gin.Context
// directly (the handler version reads from request context after injection).
func tenantIDFromGin(c *gin.Context) (string, bool) {
	tid, ok := c.Get("auth.tenant_id")
	if !ok {
		return "", false
	}
	s, ok := tid.(string)
	if s == "" {
		return "", false
	}
	return s, ok
}
