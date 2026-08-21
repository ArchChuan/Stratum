package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// MockMigrationRepo 是 port.MigrationRepo 的 testify mock（application 层测试用，
// 与 persistence 包的 pgxmock 单测职责分离）。
type MockMigrationRepo struct {
	mock.Mock
}

func (m *MockMigrationRepo) Create(ctx context.Context, tenantID string, mig *domain.MemoryMigration) (int64, error) {
	args := m.Called(ctx, tenantID, mig)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockMigrationRepo) GetActive(ctx context.Context, tenantID string) (*domain.MemoryMigration, error) {
	args := m.Called(ctx, tenantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryMigration), args.Error(1)
}

func (m *MockMigrationRepo) GetLatest(ctx context.Context, tenantID string) (*domain.MemoryMigration, error) {
	args := m.Called(ctx, tenantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryMigration), args.Error(1)
}

func (m *MockMigrationRepo) GetByID(ctx context.Context, tenantID string, id int64) (*domain.MemoryMigration, error) {
	args := m.Called(ctx, tenantID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MemoryMigration), args.Error(1)
}

func (m *MockMigrationRepo) Advance(ctx context.Context, tenantID string, id int64, progress int) (bool, error) {
	args := m.Called(ctx, tenantID, id, progress)
	return args.Bool(0), args.Error(1)
}

func (m *MockMigrationRepo) Complete(ctx context.Context, tenantID string, id int64, status domain.MigrationStatus) (bool, error) {
	args := m.Called(ctx, tenantID, id, status)
	return args.Bool(0), args.Error(1)
}

func (m *MockMigrationRepo) Restart(ctx context.Context, tenantID string, id int64) (bool, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Bool(0), args.Error(1)
}

const (
	migTestTenant = "t1"
	migFrom       = "text-embedding-v1"
	migTo         = "text-embedding-v3"
)

// testMigration 构造一条已带 ID 的迁移（status 由参数指定）。
func testMigration(status domain.MigrationStatus) *domain.MemoryMigration {
	m, _ := domain.NewMigration(migTestTenant, migFrom, migTo, 10)
	m.ID = 7
	m.Status = status
	return m
}

// newMigrationService 装配依赖桩；embedResolver / listTenants / setModel 按测试
// 需要额外注入。
func newMigrationService(t *testing.T) (*MockMigrationRepo, *MockFactRepo, *MockVectorStore, *MemoryMigrationService) {
	t.Helper()
	migRepo, factRepo, vs := new(MockMigrationRepo), new(MockFactRepo), new(MockVectorStore)
	svc := NewMemoryMigrationService(migRepo, factRepo, vs, nil)
	return migRepo, factRepo, vs, svc
}

func TestStartMigrationSwitchesModelAndReturnsRecord(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	_ = vs
	var switched string
	svc.SetEffectiveModelSetter(func(ctx context.Context, tenantID, model string) error {
		switched = model
		return nil
	})

	migRepo.On("GetActive", ctx, migTestTenant).Return(nil, nil).Once()
	factRepo.On("CountAll", ctx, migTestTenant).Return(10, nil).Once()
	migRepo.On("Create", ctx, migTestTenant, mock.AnythingOfType("*domain.MemoryMigration")).
		Run(func(args mock.Arguments) {
			m := args.Get(2).(*domain.MemoryMigration)
			require.Equal(t, migFrom, m.FromModel)
			require.Equal(t, migTo, m.ToModel)
			require.Equal(t, domain.MigrationStatusMigrating, m.Status)
			require.Equal(t, 0, m.Progress)
			require.Equal(t, 10, m.TotalFacts)
		}).Return(int64(7), nil).Once()

	got, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.ID)
	require.Equal(t, migTo, switched, "生效模型应立即切换")
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

func TestStartMigrationRejectsWhenActiveExists(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, _, svc := newMigrationService(t)
	svc.SetEffectiveModelSetter(func(context.Context, string, string) error { return nil })
	active := testMigration(domain.MigrationStatusMigrating)

	migRepo.On("GetActive", ctx, migTestTenant).Return(active, nil).Once()

	_, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.ErrorIs(t, err, domain.ErrMigrationAlreadyActive)
	migRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	factRepo.AssertNotCalled(t, "CountAll", mock.Anything, mock.Anything)
}

func TestStartMigrationCancelRecordWhenModelSwitchFails(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, _, svc := newMigrationService(t)
	switchErr := errors.New("tenant settings down")
	svc.SetEffectiveModelSetter(func(context.Context, string, string) error { return switchErr })

	migRepo.On("GetActive", ctx, migTestTenant).Return(nil, nil).Once()
	factRepo.On("CountAll", ctx, migTestTenant).Return(5, nil).Once()
	migRepo.On("Create", ctx, migTestTenant, mock.AnythingOfType("*domain.MemoryMigration")).Return(int64(7), nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusCanceled).Return(true, nil).Once()

	_, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.ErrorContains(t, err, "switch effective model")
	require.ErrorIs(t, err, switchErr)
	migRepo.AssertExpectations(t)
}

func TestStartMigrationFailsClosedWhenSetterNotWired(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, _, svc := newMigrationService(t)
	// 不注入 setModel：fail-closed，不得登记迁移。

	_, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.ErrorContains(t, err, "effective model setter is not wired")
	migRepo.AssertNotCalled(t, "GetActive", mock.Anything, mock.Anything)
	factRepo.AssertNotCalled(t, "CountAll", mock.Anything, mock.Anything)
}

// TestStartMigrationRejectsUnresolvableTargetModel 覆盖 P5 缺陷修复（fail-closed）：
// 目标模型不是目录中可解析的嵌入模型时，StartMigration 必须在登记迁移/切换生效
// 模型之前拒绝启动（返回 ErrMigrationUnknownModel → 错误中间件映射 400），避免
// 生效模型被切到无效模型，产生不可回填的僵尸迁移。
func TestStartMigrationRejectsUnresolvableTargetModel(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, _, svc := newMigrationService(t)
	svc.SetEffectiveModelSetter(func(context.Context, string, string) error { return nil })
	svc.SetModelValidator(func(ctx context.Context, tenantID, model string) error {
		return errors.New("model registry: model not resolved")
	})

	migRepo.On("GetActive", ctx, migTestTenant).Return(nil, nil).Once()
	factRepo.On("CountAll", ctx, migTestTenant).Return(5, nil).Once()

	_, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.ErrorIs(t, err, domain.ErrMigrationUnknownModel)
	// 校验失败在 Create 之前：不得落迁移记录，也不得触发 setModel 切换生效模型。
	migRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	migRepo.AssertNotCalled(t, "Complete", mock.Anything, mock.Anything, mock.Anything)
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

func TestCancelMigrationMarksCanceledAndKeepsModel(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	// 生效模型切换是确认制且不随取消回退：断言无 setModel 回调可调。
	svc.SetEffectiveModelSetter(func(context.Context, string, string) error {
		t.Fatal("cancel must not revert effective model")
		return nil
	})

	migRepo.On("GetByID", ctx, migTestTenant, int64(7)).Return(testMigration(domain.MigrationStatusMigrating), nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusCanceled).Return(true, nil).Once()

	require.NoError(t, svc.CancelMigration(ctx, migTestTenant, 7))
	migRepo.AssertExpectations(t)
}

func TestCancelMigrationRejectsDoneAndFailed(t *testing.T) {
	ctx := context.Background()
	for _, status := range []domain.MigrationStatus{domain.MigrationStatusDone, domain.MigrationStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			migRepo, _, _, svc := newMigrationService(t)
			migRepo.On("GetByID", ctx, migTestTenant, int64(7)).Return(testMigration(status), nil).Once()

			err := svc.CancelMigration(ctx, migTestTenant, 7)
			require.ErrorIs(t, err, domain.ErrMigrationNotActive)
			migRepo.AssertNotCalled(t, "Complete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestCancelMigrationIdempotentOnCanceled(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	migRepo.On("GetByID", ctx, migTestTenant, int64(7)).Return(testMigration(domain.MigrationStatusCanceled), nil).Once()

	require.NoError(t, svc.CancelMigration(ctx, migTestTenant, 7))
	migRepo.AssertNotCalled(t, "Complete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRetryMigrationRestartsFailed(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	failed := testMigration(domain.MigrationStatusFailed)
	failed.Progress = 4
	migRepo.On("GetByID", ctx, migTestTenant, int64(7)).Return(failed, nil).Once()
	migRepo.On("Restart", ctx, migTestTenant, int64(7)).Return(true, nil).Once()

	require.NoError(t, svc.RetryMigration(ctx, migTestTenant, 7))
	migRepo.AssertExpectations(t)
}

func TestRetryMigrationRejectsMigrating(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	migRepo.On("GetByID", ctx, migTestTenant, int64(7)).Return(testMigration(domain.MigrationStatusMigrating), nil).Once()

	err := svc.RetryMigration(ctx, migTestTenant, 7)
	require.ErrorIs(t, err, domain.ErrMigrationNotRetryable)
	migRepo.AssertNotCalled(t, "Restart", mock.Anything, mock.Anything, mock.Anything)
}

func TestGetCurrentReturnsLatestMigration(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	expected := testMigration(domain.MigrationStatusDone)
	migRepo.On("GetLatest", ctx, migTestTenant).Return(expected, nil).Once()

	got, err := svc.GetCurrent(ctx, migTestTenant)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.ID)
	require.Equal(t, domain.MigrationStatusDone, got.Status)
	migRepo.AssertExpectations(t)
}

func TestCostPreviewComputesFactsAndSeconds(t *testing.T) {
	ctx := context.Background()
	_, factRepo, _, svc := newMigrationService(t)
	factRepo.On("CountAll", ctx, migTestTenant).Return(10, nil).Once()

	cost, err := svc.CostPreview(ctx, migTestTenant)
	require.NoError(t, err)
	require.Equal(t, 10, cost.FactCount)
	// ceil(10 × 200ms / 1000) = 2 秒。
	require.Equal(t, int64(2), cost.EstimatedSeconds)
	factRepo.AssertExpectations(t)
}

func TestCostPreviewPropagatesCountError(t *testing.T) {
	ctx := context.Background()
	_, factRepo, _, svc := newMigrationService(t)
	wantErr := errors.New("db down")
	factRepo.On("CountAll", ctx, migTestTenant).Return(0, wantErr).Once()

	_, err := svc.CostPreview(ctx, migTestTenant)
	require.ErrorIs(t, err, wantErr)
	require.ErrorContains(t, err, "cost preview")
}

// makeFacts 生成 n 条内容唯一的 MemoryFact，便于断言幂等 Upsert 的 key。
func makeFacts(n int) []*domain.MemoryFact {
	facts := make([]*domain.MemoryFact, 0, n)
	for i := 0; i < n; i++ {
		f, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser),
			"fact content "+string(rune('a'+i%26)), 0.8, nil)
		f.ID = "fact-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i))
		facts = append(facts, f)
	}
	return facts
}

// stubEmbedResolver 返回同一个假 embedder；回填只依赖 Embed()，不调用 Model()。
func stubEmbedResolver(embed *MockEmbedClient) EmbedClientResolverByModel {
	return func(ctx context.Context, tenantID, model string) port.EmbedClient {
		return embed
	}
}

func TestProcessPendingBackfillsSinglePageAndCompletes(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	embed := new(MockEmbedClient)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 2
	facts := makeFacts(2)
	upserted := map[string][]*port.VectorDoc{}

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, constants.MemoryMigrationPageSize, 0).Return(facts, nil).Once()
	embed.On("Embed", ctx, "fact content a").Return([]float32{0.1, 0.2}, nil)
	embed.On("Embed", ctx, "fact content b").Return([]float32{0.1, 0.2}, nil)
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(nil).
		Run(func(args mock.Arguments) {
			collection := args.Get(1).(string)
			docs := args.Get(2).([]*port.VectorDoc)
			upserted[collection] = append(upserted[collection], docs...)
		}).Once()
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 2).Return(true, nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusDone).Return(true, nil).Once()

	svc.ProcessPending(ctx)

	// 集合名 = factsCollectionName(tenant, toModel)（模型后缀、SanitizeMilvusName）。
	collection := "memory_facts_t1_text_embedding_v3"
	require.Len(t, upserted[collection], 2, "两条事实应幂等 Upsert 到 B 集合")
	gotIDs := map[string]bool{}
	for _, d := range upserted[collection] {
		gotIDs[d.ID] = true
		require.NotEmpty(t, d.Embedding)
		require.Equal(t, "user1", d.Metadata["user_id"])
	}
	for _, f := range facts {
		require.True(t, gotIDs[f.ID], "fact %s 必须被回填", f.ID)
	}
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
	vs.AssertExpectations(t)
	embed.AssertExpectations(t)
}

func TestProcessPendingPaginatesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	upserts := 0
	embed := new(MockEmbedClient)
	embed.On("Embed", ctx, mock.Anything).Return([]float32{0.1, 0.2}, nil)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 60
	facts := makeFacts(60)

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 0).Return(facts[:50], nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 50).Return(facts[50:], nil).Once()
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		upserts += len(args.Get(2).([]*port.VectorDoc))
	}).Twice()
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 50).Return(true, nil).Once()
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 60).Return(true, nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusDone).Return(true, nil).Once()

	svc.ProcessPending(ctx)

	require.Equal(t, 60, upserts)
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

func TestProcessPendingMarksFailedOnUpsertError(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	embed := new(MockEmbedClient)
	embed.On("Embed", ctx, mock.Anything).Return([]float32{0.1, 0.2}, nil)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 1
	wantErr := errors.New("milvus unavailable")

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 0).Return(makeFacts(1), nil).Once()
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(wantErr).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusFailed).Return(true, nil).Once()

	svc.ProcessPending(ctx)

	migRepo.AssertNotCalled(t, "Advance", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	migRepo.AssertExpectations(t)
}

func TestProcessPendingMarksFailedOnEmbedError(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	embed := new(MockEmbedClient)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 1
	wantErr := errors.New("embedding api timeout")

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 0).Return(makeFacts(1), nil).Once()
	embed.On("Embed", ctx, mock.Anything).Return(nil, wantErr).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusFailed).Return(true, nil).Once()

	svc.ProcessPending(ctx)

	vs.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything, mock.Anything)
	migRepo.AssertExpectations(t)
}

func TestProcessPendingStopsWhenAdvanceMisses(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	embed := new(MockEmbedClient)
	embed.On("Embed", ctx, mock.Anything).Return([]float32{0.1, 0.2}, nil)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 2

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 0).Return(makeFacts(2), nil).Once()
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(nil).Once()
	// 并发取消：Advance 未命中 migrating 守卫 → 停止回填，不得标记完成。
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 2).Return(false, nil).Once()

	svc.ProcessPending(ctx)

	migRepo.AssertNotCalled(t, "Complete", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	migRepo.AssertExpectations(t)
}

func TestProcessPendingHandlesEmptyPageAsDone(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, _, svc := newMigrationService(t)
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 2

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	// 快照漂移：total 按快照，但期间 facts 被清理 → 空页即无可回填，直接完成。
	factRepo.On("ListAllFacts", ctx, migTestTenant, 50, 0).Return(nil, nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusDone).Return(true, nil).Once()

	svc.ProcessPending(ctx)
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
}

func TestProcessPendingContinuesAfterTenantError(t *testing.T) {
	ctx := context.Background()
	migRepo, _, _, svc := newMigrationService(t)
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{"t1", "t2"}, nil })

	migRepo.On("GetActive", ctx, "t1").Return(nil, errors.New("db down")).Once()
	migRepo.On("GetActive", ctx, "t2").Return(nil, nil).Once()

	svc.ProcessPending(ctx)
	migRepo.AssertExpectations(t)
}

func TestProcessPendingStopsWhenNoTenantLister(t *testing.T) {
	svc := NewMemoryMigrationService(new(MockMigrationRepo), new(MockFactRepo), new(MockVectorStore), nil)
	// 未注入 lister：告警跳过，不得 panic。
	svc.ProcessPending(context.Background())
}

func TestMigrationWorkerGracefulShutdown(t *testing.T) {
	_, _, _, svc := newMigrationService(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Start(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("migration worker did not stop within 1s")
	}
}

// migrationMetricsSpy 记录迁移进度/停滞指标调用（P6），嵌入 NoopMetrics 满足
// MetricsProvider 其余方法。
type migrationMetricsSpy struct {
	observability.NoopMetrics
	progress     []migrationProgressCall
	stalledCalls int
}

type migrationProgressCall struct {
	tenantID, from, to, status string
	progress                   int
}

func (s *migrationMetricsSpy) SetMemoryMigrationProgress(tenantID, from, to, status string, progress int) {
	s.progress = append(s.progress, migrationProgressCall{tenantID, from, to, status, progress})
}

func (s *migrationMetricsSpy) IncMemoryMigrationStalled(tenantID, from, to string) { s.stalledCalls++ }

// TestMigrationReportsProgressMetrics 验证进度 gauge 在启动（0/migrating）、
// 回填推进（next/migrating）与完成（total/done）各阶段上报。
func TestMigrationReportsProgressMetrics(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	spy := &migrationMetricsSpy{}
	svc.SetMetrics(spy)
	svc.SetEffectiveModelSetter(func(ctx context.Context, tenantID, model string) error { return nil })
	embed := new(MockEmbedClient)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	// StartMigration → progress=0/migrating
	migRepo.On("GetActive", ctx, migTestTenant).Return(nil, nil).Once()
	factRepo.On("CountAll", ctx, migTestTenant).Return(10, nil).Once()
	migRepo.On("Create", ctx, migTestTenant, mock.AnythingOfType("*domain.MemoryMigration")).Return(int64(7), nil).Once()
	_, err := svc.StartMigration(ctx, migTestTenant, migFrom, migTo)
	require.NoError(t, err)
	require.Equal(t, []migrationProgressCall{{migTestTenant, migFrom, migTo, "migrating", 0}}, spy.progress)

	// ProcessPending 回填 3 条 → progress=3/migrating，然后空页 → done/total=10
	facts := makeFacts(3)
	embed.On("Embed", ctx, facts[0].Content).Return([]float32{0.1, 0.2}, nil)
	embed.On("Embed", ctx, facts[1].Content).Return([]float32{0.1, 0.2}, nil)
	embed.On("Embed", ctx, facts[2].Content).Return([]float32{0.1, 0.2}, nil)
	migRepo.On("GetActive", ctx, migTestTenant).Return(testMigration(domain.MigrationStatusMigrating), nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, constants.MemoryMigrationPageSize, 0).
		Return(facts, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, constants.MemoryMigrationPageSize, 3).
		Return(nil, nil).Once()
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(nil).Once()
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 3).Return(true, nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusDone).Return(true, nil).Once()
	svc.ProcessPending(ctx)

	require.Len(t, spy.progress, 3)
	require.Equal(t, migrationProgressCall{migTestTenant, migFrom, migTo, "migrating", 3}, spy.progress[1])
	// 快照 total=10 但有漂移（存量 3 条）：空页即完成，游标停在实际回填的 3。
	require.Equal(t, migrationProgressCall{migTestTenant, migFrom, migTo, "done", 3}, spy.progress[2])
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
	vs.AssertExpectations(t)
}

func TestBackfillSkipsNonActiveFacts(t *testing.T) {
	ctx := context.Background()
	migRepo, factRepo, vs, svc := newMigrationService(t)
	embed := new(MockEmbedClient)
	svc.SetEmbedResolver(stubEmbedResolver(embed))
	svc.SetTenantLister(func(context.Context) ([]string, error) { return []string{migTestTenant}, nil })

	m := testMigration(domain.MigrationStatusMigrating)
	m.TotalFacts = 2
	facts := makeFacts(2)
	facts[1].Status = domain.FactStatusSuperseded
	upserted := map[string][]*port.VectorDoc{}

	migRepo.On("GetActive", ctx, migTestTenant).Return(m, nil).Once()
	factRepo.On("ListAllFacts", ctx, migTestTenant, constants.MemoryMigrationPageSize, 0).Return(facts, nil).Once()
	embed.On("Embed", ctx, "fact content a").Return([]float32{0.1, 0.2}, nil)
	vs.On("Upsert", ctx, mock.Anything, mock.Anything).Return(nil).
		Run(func(args mock.Arguments) {
			collection := args.Get(1).(string)
			docs := args.Get(2).([]*port.VectorDoc)
			upserted[collection] = append(upserted[collection], docs...)
		}).Once()
	migRepo.On("Advance", ctx, migTestTenant, int64(7), 2).Return(true, nil).Once()
	migRepo.On("Complete", ctx, migTestTenant, int64(7), domain.MigrationStatusDone).Return(true, nil).Once()

	svc.ProcessPending(ctx)

	collection := "memory_facts_t1_text_embedding_v3"
	require.Len(t, upserted[collection], 1, "superseded/archived 事实不得被迁移回填复活")
	require.Equal(t, facts[0].ID, upserted[collection][0].ID)
	migRepo.AssertExpectations(t)
	factRepo.AssertExpectations(t)
	vs.AssertExpectations(t)
}

// TestTrackStallIncrementsCounterOnNoProgress 验证连续扫描间隔间进度未推进时
// 上报停滞计数器；迁移进入终态后停止跟踪。
func TestTrackStallIncrementsCounterOnNoProgress(t *testing.T) {
	spy := &migrationMetricsSpy{}
	_, _, _, svc := newMigrationService(t)
	svc.SetMetrics(spy)

	m := testMigration(domain.MigrationStatusMigrating)
	m.Progress = 3
	svc.trackStall(m) // 第一拍：记基线
	svc.trackStall(m) // 第二拍：未推进 → 停滞
	require.Equal(t, 1, spy.stalledCalls)
	svc.trackStall(m) // 第三拍：仍未推进 → 再累计
	require.Equal(t, 2, spy.stalledCalls)

	m.Status = domain.MigrationStatusDone
	svc.trackStall(m) // 终态：停止跟踪
	require.Equal(t, 2, spy.stalledCalls)

	// 终态后再跟踪同 key 不会复活：重新 migrating 才恢复基线。
	m.Status = domain.MigrationStatusMigrating
	svc.trackStall(m)
	require.Equal(t, 2, spy.stalledCalls)
}
