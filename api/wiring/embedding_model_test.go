package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

// fakeTenantRepo 实现 TenantService 需要的 settings 读写，其余 TenantRepo
// 方法返回零值。settingsJSON 存整个 settings map 的原始 JSON（与生产一致）。
type fakeTenantRepo struct {
	settingsJSON  []byte
	settingsErr   error
	lastWriteJSON []byte
}

func (r *fakeTenantRepo) CountMembers(context.Context, string) (int, error) { return 0, nil }
func (r *fakeTenantRepo) ListMembers(context.Context, string, int, int) ([]iamdomain.Member, error) {
	return nil, nil
}
func (r *fakeTenantRepo) ListMembersByRole(context.Context, string, []string) ([]iamdomain.Member, error) {
	return nil, nil
}
func (r *fakeTenantRepo) GetMemberRole(context.Context, string, string) (string, error) {
	return "", nil
}
func (r *fakeTenantRepo) UpdateMemberRole(context.Context, string, string, string) error { return nil }
func (r *fakeTenantRepo) DeleteMember(context.Context, string, string) error             { return nil }
func (r *fakeTenantRepo) GetTenantSettings(context.Context, string) (string, bool, []byte, error) {
	return "", false, r.settingsJSON, r.settingsErr
}
func (r *fakeTenantRepo) UpdateTenantName(context.Context, string, string) error { return nil }
func (r *fakeTenantRepo) UpdateTenantSettings(_ context.Context, _ string, settingsJSON []byte) error {
	r.lastWriteJSON = append([]byte(nil), settingsJSON...)
	return nil
}
func (r *fakeTenantRepo) ListUserTenants(context.Context, string) ([]iamdomain.UserTenantInfo, error) {
	return nil, nil
}

var _ iamport.TenantRepo = (*fakeTenantRepo)(nil)

// newTestTenantEmbeddingResolver 构造一个租户 settings 里含给定配置的
// tenantEmbeddingModelResolver，registry 复用 knowledge 目录。
func newTestTenantEmbeddingResolver(
	settings map[string]any,
	registry *llmgateway.ModelRegistry,
) *tenantEmbeddingModelResolver {
	raw, err := json.Marshal(settings)
	if err != nil {
		panic(err)
	}
	tenantSvc := iamapp.NewTenantService(&fakeTenantRepo{settingsJSON: raw}, zap.NewNop())
	return newTenantEmbeddingModelResolver(
		func() *iamapp.TenantService { return tenantSvc },
		registry,
		zap.NewNop(),
	)
}

func TestResolveMemoryEmbeddingModelFailClosed(t *testing.T) {
	registry := newKnowledgeRegistry([]domain.Model{{
		ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding",
		Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding},
	}})
	ctx := context.Background()

	t.Run("tenant service unavailable", func(t *testing.T) {
		r := &tenantEmbeddingModelResolver{registry: registry, logger: zap.NewNop()}
		_, err := r.ResolveMemoryEmbeddingModel(ctx, "t1")
		require.ErrorIs(t, err, errMemoryEmbeddingNotConfigured)
	})
	t.Run("key missing", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"other": "x"}, registry)
		_, err := r.ResolveMemoryEmbeddingModel(ctx, "t1")
		require.ErrorIs(t, err, errMemoryEmbeddingNotConfigured)
	})
	t.Run("key present but empty", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"memory_embedding_model": "  "}, registry)
		_, err := r.ResolveMemoryEmbeddingModel(ctx, "t1")
		require.ErrorIs(t, err, errMemoryEmbeddingNotConfigured)
	})
	t.Run("model absent from catalogue", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"memory_embedding_model": "ghost"}, registry)
		_, err := r.ResolveMemoryEmbeddingModel(ctx, "t1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "resolve model")
	})
	t.Run("settings read failure propagates fail-closed", func(t *testing.T) {
		tenantSvc := iamapp.NewTenantService(
			&fakeTenantRepo{settingsErr: errors.New("db down")}, zap.NewNop())
		r := newTenantEmbeddingModelResolver(func() *iamapp.TenantService { return tenantSvc }, registry, zap.NewNop())
		_, err := r.ResolveMemoryEmbeddingModel(ctx, "t1")
		require.Error(t, err)
		require.Contains(t, err.Error(), "read tenant settings")
	})
}

func TestResolveMemoryEmbeddingModelSuccess(t *testing.T) {
	registry := newKnowledgeRegistry([]domain.Model{{
		ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding",
		Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding},
	}})
	r := newTestTenantEmbeddingResolver(
		map[string]any{"memory_embedding_model": "managed-embedding"}, registry)

	model, err := r.ResolveMemoryEmbeddingModel(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, "managed-embedding", model)
}

func TestIsMemoryEmbeddingModelConfigured(t *testing.T) {
	registry := newKnowledgeRegistry(nil)
	ctx := context.Background()

	t.Run("key present and non-empty", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"memory_embedding_model": "x"}, registry)
		require.True(t, r.IsMemoryEmbeddingModelConfigured(ctx, "t1"))
	})
	t.Run("key missing", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{}, registry)
		require.False(t, r.IsMemoryEmbeddingModelConfigured(ctx, "t1"))
	})
	t.Run("key blank", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"memory_embedding_model": " "}, registry)
		require.False(t, r.IsMemoryEmbeddingModelConfigured(ctx, "t1"))
	})
	t.Run("settings read failure", func(t *testing.T) {
		tenantSvc := iamapp.NewTenantService(
			&fakeTenantRepo{settingsErr: errors.New("db down")}, zap.NewNop())
		r := newTenantEmbeddingModelResolver(func() *iamapp.TenantService { return tenantSvc }, registry, zap.NewNop())
		require.False(t, r.IsMemoryEmbeddingModelConfigured(ctx, "t1"))
	})
	t.Run("tenant service nil", func(t *testing.T) {
		r := &tenantEmbeddingModelResolver{registry: registry, logger: zap.NewNop()}
		require.False(t, r.IsMemoryEmbeddingModelConfigured(ctx, "t1"))
	})
}

func TestBuildEmbedResolverUsesTenantConfig(t *testing.T) {
	registry := newKnowledgeRegistry([]domain.Model{{
		ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding",
		Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding},
	}})
	ctx := context.Background()

	t.Run("configured model resolves to a client", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(
			map[string]any{"memory_embedding_model": "managed-embedding"}, registry)
		require.NotNil(t, buildEmbedResolver(r, zap.NewNop())(ctx, "t1"))
	})
	t.Run("unconfigured tenant fails closed to nil", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(nil, registry)
		require.Nil(t, buildEmbedResolver(r, zap.NewNop())(ctx, "t1"))
	})
	t.Run("misconfigured model fails closed to nil", func(t *testing.T) {
		r := newTestTenantEmbeddingResolver(map[string]any{"memory_embedding_model": "ghost"}, registry)
		require.Nil(t, buildEmbedResolver(r, zap.NewNop())(ctx, "t1"))
	})
}

// newSeedTestContainer 组装 seed 依赖：租户列表来自真实 persistence.TenantRepo
// （pgxmock），settings 读写走 fakeTenantRepo（TenantService）。mock 返回所有
// 活跃租户 id，测试负责按需构建种子数据。
func newSeedTestContainer(t *testing.T, tenantIDs []string, repo *fakeTenantRepo) (*Container, pgxmock.PgxPoolIface) {
	t.Helper()
	mockPool, err := pgxmock.NewPool()
	require.NoError(t, err)
	rows := pgxmock.NewRows([]string{"id"})
	for _, id := range tenantIDs {
		rows.AddRow(id)
	}
	mockPool.ExpectQuery(`SELECT id FROM public.tenants WHERE deleted_at IS NULL`).
		WillReturnRows(rows)

	registry := newKnowledgeRegistry([]domain.Model{{
		ID: "embedding-1", ProviderID: "provider-1", Name: "managed-embedding",
		Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding},
	}})
	tenantSvc := iamapp.NewTenantService(repo, zap.NewNop())
	resolver := newTenantEmbeddingModelResolver(
		func() *iamapp.TenantService { return tenantSvc }, registry, zap.NewNop())
	return &Container{
		Logger: zap.NewNop(),
		IAM: &IAM{
			TenantService: tenantSvc,
			TenantRepo:    iampersistence.NewTenantRepo(mockPool),
		},
		LLMGateway: &LLMGateway{
			Registry:                registry,
			TenantEmbeddingResolver: resolver,
		},
	}, mockPool
}

func TestSeedMemoryEmbeddingModelsBackfillsOnlyMissingKeys(t *testing.T) {
	t.Run("backfills unconfigured tenant idempotently preserving other keys", func(t *testing.T) {
		repo := &fakeTenantRepo{settingsJSON: []byte(`{"a":1}`)}
		c, mock := newSeedTestContainer(t, []string{"t1"}, repo)
		require.NoError(t, c.seedMemoryEmbeddingModels(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		require.Contains(t, string(repo.lastWriteJSON), `"memory_embedding_model":"managed-embedding"`)
		require.Contains(t, string(repo.lastWriteJSON), `"a":1`) // 不覆盖其他键
	})
	t.Run("skips already configured tenant", func(t *testing.T) {
		repo := &fakeTenantRepo{settingsJSON: []byte(`{"memory_embedding_model":"custom"}`)}
		c, mock := newSeedTestContainer(t, []string{"t1"}, repo)
		require.NoError(t, c.seedMemoryEmbeddingModels(context.Background()))
		require.NoError(t, mock.ExpectationsWereMet())
		require.Nil(t, repo.lastWriteJSON) // 未触发写入
	})
	t.Run("no global default model skips seed without error", func(t *testing.T) {
		repo := &fakeTenantRepo{settingsJSON: []byte(`{"a":1}`)}
		c, _ := newSeedTestContainer(t, []string{"t1"}, repo)
		// 替换为空目录 registry → ResolveDefaultEmbeddingModel 返回 ("", nil) → seed 跳过。
		empty := newKnowledgeRegistry(nil)
		tenantSvc := iamapp.NewTenantService(repo, zap.NewNop())
		resolver := newTenantEmbeddingModelResolver(
			func() *iamapp.TenantService { return tenantSvc }, empty, zap.NewNop())
		c.LLMGateway.Registry = empty
		c.LLMGateway.TenantEmbeddingResolver = resolver
		require.NoError(t, c.seedMemoryEmbeddingModels(context.Background()))
		require.Nil(t, repo.lastWriteJSON) // 未触发写入
	})
}
