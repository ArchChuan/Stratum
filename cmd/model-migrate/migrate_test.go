package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

func nopMigrate(ctx context.Context, pool pgxPool, logger *zap.Logger, dryRun bool) error {
	return nil
}

func TestRun_requiresPostgresURL(t *testing.T) {
	err := run([]string{}, func(string) string { return "" }, zap.NewNop(), nopMigrate)
	require.ErrorContains(t, err, "POSTGRES_URL is required")
}

func TestRun_flagParseError(t *testing.T) {
	err := run([]string{"-nope"}, func(string) string { return "postgres://test" }, zap.NewNop(), nopMigrate)
	require.ErrorContains(t, err, "parse flags")
}

func TestRun_dryRunDefaultsTrue(t *testing.T) {
	got := false
	err := run([]string{}, func(string) string { return "postgres://test" }, zap.NewNop(),
		func(ctx context.Context, pool pgxPool, logger *zap.Logger, dryRun bool) error {
			got = dryRun
			return nil
		})
	require.NoError(t, err)
	require.True(t, got, "dry-run must default to true")
}

func provider(id, name, apiKey string, enabled bool, updated time.Time) tenantProvider {
	return tenantProvider{
		id: id, name: name, kind: "openai", baseURL: "https://" + id + ".example.com",
		apiKey: apiKey, enabled: enabled, createdAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		updatedAt: updated,
	}
}

func model(providerName, name string, defEmbed bool, createdAt time.Time) tenantModel {
	return tenantModel{
		providerName: providerName, name: name, displayName: name,
		capabilities: []string{"chat"}, defaultEmbedding: defEmbed, createdAt: createdAt,
	}
}

// TestMergePlan_providerKeyConflictTakesEnabled 同 name 不同 key 冲突取 enabled。
func TestMergePlan_providerKeyConflictTakesEnabled(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	res := mergePlan([]tenantProvider{
		provider("p-disabled", "shared", "sk-old", false, newer),
		provider("p-enabled", "shared", "sk-new", true, older),
	}, nil)
	require.Len(t, res.providers, 1)
	got := res.providers["shared"]
	require.NotNil(t, got)
	require.True(t, got.enabled, "must prefer enabled provider")
	require.Equal(t, "sk-new", got.apiKey)
	require.Len(t, res.warnings, 1)
}

// TestMergePlan_providerKeyConflictLatestUpdated 同 enabled 状态取 updated_at 最新。
func TestMergePlan_providerKeyConflictLatestUpdated(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	res := mergePlan([]tenantProvider{
		provider("p-old", "shared", "sk-old", true, older),
		provider("p-new", "shared", "sk-new", true, newer),
	}, nil)
	got := res.providers["shared"]
	require.NotNil(t, got)
	require.Equal(t, "sk-new", got.apiKey, "same enabled state must pick latest updated_at")
	require.Equal(t, 2, got.keys)
	require.Len(t, res.warnings, 1)
}

// TestMergePlan_providerSameKeyNoWarning 同 name 同 key 视为重复数据，不告警。
func TestMergePlan_providerSameKeyNoWarning(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res := mergePlan([]tenantProvider{
		provider("p1", "dup", "sk-same", true, ts),
		provider("p2", "dup", "sk-same", true, ts),
	}, nil)
	require.Len(t, res.providers, 1)
	require.Empty(t, res.warnings, "identical keys are not a conflict")
}

// TestMergePlan_defaultEmbeddingConflictKeepsFirstCreated 同 (provider,name) 双 default_embedding
// 保留先创建者，清后创建者标记并告警。
func TestMergePlan_defaultEmbeddingConflictKeepsFirstCreated(t *testing.T) {
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	res := mergePlan(nil, []tenantModel{
		model("openai", "embed-a", true, earlier),
		model("openai", "embed-a", true, later),
	})
	require.Len(t, res.models, 1)
	got := res.models["openai"+providerKeySeparator+"embed-a"]
	require.NotNil(t, got)
	require.True(t, got.defaultEmbedding, "first created keeps the mark")
	require.Equal(t, earlier, got.createdAt)
	require.Len(t, res.warnings, 1)
	require.Contains(t, res.warnings[0], "default_embedding 冲突")
}

// TestMergePlan_noConflictMergesNormally 无冲突归并保留全部 provider/model。
func TestMergePlan_noConflictMergesNormally(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res := mergePlan([]tenantProvider{
		provider("p1", "openai", "sk-1", true, ts),
		provider("p2", "zhipu", "sk-2", true, ts),
	}, []tenantModel{
		model("openai", "gpt-4o", false, ts),
		model("zhipu", "glm-4", true, ts),
	})
	require.Len(t, res.providers, 2)
	require.Len(t, res.models, 2)
	require.Empty(t, res.warnings)
	require.True(t, res.models["zhipu"+providerKeySeparator+"glm-4"].defaultEmbedding)
}

// legacyTenantTables 在测试租户 schema 建迁移前的存量 providers/models 表。
// 注意：pgx 的 Exec 走 extended protocol，不支持单条多语句，必须逐条执行。
func legacyTenantTables(t *testing.T, pool *pgxpool.Pool, schema string) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS "%s".providers (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT NOT NULL,
			kind TEXT NOT NULL, base_url TEXT NOT NULL DEFAULT '', api_key TEXT NOT NULL DEFAULT '',
			default_model TEXT NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(tenant_id, name))`, schema))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS "%s".models (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, provider_id TEXT NOT NULL
				REFERENCES "%s".providers(id) ON DELETE CASCADE,
			name TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', capabilities TEXT[] NOT NULL DEFAULT '{}',
			context_window INT NOT NULL DEFAULT 0, max_tokens INT NOT NULL DEFAULT 0,
			input_price DOUBLE PRECISION NOT NULL DEFAULT 0, output_price DOUBLE PRECISION NOT NULL DEFAULT 0,
			recommended BOOLEAN NOT NULL DEFAULT false, enabled BOOLEAN NOT NULL DEFAULT true,
			provider_managed BOOLEAN NOT NULL DEFAULT false,
			default_embedding BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(tenant_id, provider_id, name))`, schema, schema))
	require.NoError(t, err)
}

// publicTables 确保 public 平台目录存在（对齐迁移 035 结构），供真实链路测试。
func publicTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.providers (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
			base_url TEXT NOT NULL DEFAULT '', api_key TEXT NOT NULL DEFAULT '',
			default_model TEXT NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.models (
			id TEXT PRIMARY KEY, provider_id TEXT NOT NULL REFERENCES public.providers(id) ON DELETE CASCADE,
			name TEXT NOT NULL, display_name TEXT NOT NULL DEFAULT '', capabilities TEXT[] NOT NULL DEFAULT '{}',
			context_window INT NOT NULL DEFAULT 0, max_tokens INT NOT NULL DEFAULT 0,
			input_price DOUBLE PRECISION NOT NULL DEFAULT 0, output_price DOUBLE PRECISION NOT NULL DEFAULT 0,
			recommended BOOLEAN NOT NULL DEFAULT false, enabled BOOLEAN NOT NULL DEFAULT true,
			provider_managed BOOLEAN NOT NULL DEFAULT false, default_embedding BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(provider_id, name))`)
	require.NoError(t, err)
}

func TestMigrate_realRoundTrip(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	publicTables(t, pool)

	// 租户 A：openai(sk-1) + zhipu(sk-3)，模型 gpt-4o/glm-4/embed-3(default_embedding)。
	tenantA := postgrestest.CreateTestTenant(t, pool)
	legacyTenantTables(t, pool, "tenant_"+tenantA)
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "tenant_%s".providers (id, tenant_id, name, kind, base_url, api_key, enabled)
		VALUES ('p1', $1, 'openai', 'openai', 'https://openai.example.com', 'enc:sk-1', true),
		       ('p3', $1, 'zhipu', 'zhipu', 'https://zhipu.example.com', 'enc:sk-3', true)`, tenantA), tenantA)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "tenant_%s".models (id, tenant_id, provider_id, name, capabilities, default_embedding)
		VALUES ('m1', $1, 'p1', 'gpt-4o', '{chat}', false),
		       ('m2', $1, 'p3', 'glm-4', '{chat}', false),
		       ('m3', $1, 'p3', 'embed-3', '{embedding}', true)`, tenantA), tenantA)
	require.NoError(t, err)

	// 租户 B：同 name openai 但不同 key（跨租户冲突），模型 gpt-4o 同名同 provider 重复。
	tenantB := postgrestest.CreateTestTenant(t, pool)
	legacyTenantTables(t, pool, "tenant_"+tenantB)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "tenant_%s".providers (id, tenant_id, name, kind, base_url, api_key, enabled)
		VALUES ('p1', $1, 'openai', 'openai', 'https://openai.example.com', 'enc:sk-2', true)`, tenantB), tenantB)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "tenant_%s".models (id, tenant_id, provider_id, name, capabilities, default_embedding)
		VALUES ('m1', $1, 'p1', 'gpt-4o', '{chat}', false)`, tenantB), tenantB)
	require.NoError(t, err)

	require.NoError(t, migrate(ctx, pool, zap.NewNop(), false))

	var providers, models int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.providers WHERE name IN ('openai','zhipu')`).Scan(&providers))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.models
		 WHERE name IN ('gpt-4o','glm-4','embed-3') AND provider_id IN
		   (SELECT id FROM public.providers WHERE name IN ('openai','zhipu'))`).Scan(&models))
	require.Equal(t, 2, providers, "cross-tenant openai key conflict must merge to one public provider")
	require.Equal(t, 3, models, "cross-tenant duplicate gpt-4o must merge into one public model")

	var defEmbed bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT default_embedding FROM public.models WHERE name='embed-3'`).Scan(&defEmbed))
	require.True(t, defEmbed, "embedding model must keep its default_embedding mark")

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.models WHERE name IN ('gpt-4o','glm-4','embed-3')`)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM public.providers WHERE name IN ('openai','zhipu')`)
	})
}

func TestMigrate_dryRunDoesNotWrite(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	publicTables(t, pool)

	tenantID := postgrestest.CreateTestTenant(t, pool)
	schema := "tenant_" + tenantID
	legacyTenantTables(t, pool, schema)

	_, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".providers (id, tenant_id, name, kind, base_url, api_key, enabled)
		VALUES ('p1', $1, 'qwen', 'qwen', 'https://qwen.example.com', 'enc:sk-qwen', true)`, schema), tenantID)
	require.NoError(t, err)

	require.NoError(t, migrate(ctx, pool, zap.NewNop(), true))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.providers WHERE name='qwen'`).Scan(&n))
	require.Zero(t, n, "dry-run must not write public providers")
}

// TestMigrate_preexistingPublicProviderReusesRealID 平台目录已有存量 provider（035 已应用）
// 时，迁移必须复用其真实 id 而非归并期新 UUID，否则 model 的 FK 违反；且不重复插入。
func TestMigrate_preexistingPublicProviderReusesRealID(t *testing.T) {
	pool := postgrestest.NewPool(t)
	ctx := context.Background()
	publicTables(t, pool)

	// 模拟 035 已应用且平台目录已有存量 openai（真实部署顺序：迁移工具在 035 之后跑）。
	_, err := pool.Exec(ctx, `
		INSERT INTO public.providers (id, name, kind, base_url, api_key, default_model, enabled)
		VALUES ('pre-existing-id', 'openai', 'openai', 'https://openai.example.com',
		        'enc:sk-prod', 'gpt-4o', true)
		ON CONFLICT (name) DO NOTHING`)
	require.NoError(t, err)

	tenantID := postgrestest.CreateTestTenant(t, pool)
	schema := "tenant_" + tenantID
	legacyTenantTables(t, pool, schema)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".providers (id, tenant_id, name, kind, base_url, api_key, enabled)
		VALUES ('p1', $1, 'openai', 'openai', 'https://openai.example.com', 'enc:sk-tenant', true)`, schema), tenantID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".models (id, tenant_id, provider_id, name, capabilities)
		VALUES ('m1', $1, 'p1', 'gpt-4o', '{chat}')`, schema), tenantID)
	require.NoError(t, err)

	require.NoError(t, migrate(ctx, pool, zap.NewNop(), false))

	var providerCount, modelCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.providers WHERE name='openai'`).Scan(&providerCount))
	require.Equal(t, 1, providerCount, "preexisting provider must not be duplicated")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.models WHERE name='gpt-4o' AND provider_id='pre-existing-id'`).Scan(&modelCount))
	require.Equal(t, 1, modelCount, "model must reference the real public provider id")

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.models WHERE name='gpt-4o'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.providers WHERE name='openai'`)
	})
}
