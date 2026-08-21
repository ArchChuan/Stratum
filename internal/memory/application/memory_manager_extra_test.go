package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	memdomain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// mockMemoryRepo 是 MemoryRepo 的 testify mock。
type mockMemoryRepo struct {
	mock.Mock
}

func (m *mockMemoryRepo) Add(ctx context.Context, entry *memdomain.MemoryEntry) error {
	args := m.Called(ctx, entry)
	return args.Error(0)
}

func (m *mockMemoryRepo) Get(ctx context.Context, tenantID, id string) (*memdomain.MemoryEntry, error) {
	args := m.Called(ctx, tenantID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memdomain.MemoryEntry), args.Error(1)
}

func (m *mockMemoryRepo) Search(ctx context.Context, tenantID, userID, query string, limit int) ([]*memdomain.MemoryEntry, error) {
	args := m.Called(ctx, tenantID, userID, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*memdomain.MemoryEntry), args.Error(1)
}

func (m *mockMemoryRepo) Delete(ctx context.Context, tenantID, id string) error {
	args := m.Called(ctx, tenantID, id)
	return args.Error(0)
}

func (m *mockMemoryRepo) ClearSession(ctx context.Context, tenantID, sessionID string) error {
	args := m.Called(ctx, tenantID, sessionID)
	return args.Error(0)
}

func (m *mockMemoryRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	args := m.Called(ctx, tenantID, userID)
	return args.Error(0)
}

func (m *mockMemoryRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	args := m.Called(ctx, tenantID, agentID)
	return args.Error(0)
}

func (m *mockMemoryRepo) ListExpired(ctx context.Context, tenantID string, now, createdBefore time.Time, limit int) ([]string, error) {
	args := m.Called(ctx, tenantID, now, createdBefore, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockMemoryRepo) DeleteByIDs(ctx context.Context, tenantID string, ids []string) error {
	args := m.Called(ctx, tenantID, ids)
	return args.Error(0)
}

func (m *mockMemoryRepo) Stats(ctx context.Context, tenantID string) (*memdomain.MemoryStats, error) {
	args := m.Called(ctx, tenantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*memdomain.MemoryStats), args.Error(1)
}

func (m *mockMemoryRepo) GetSummary(ctx context.Context, tenantID, sessionID string) (string, error) {
	args := m.Called(ctx, tenantID, sessionID)
	return args.String(0), args.Error(1)
}

func entry(id string, importance float64) *memdomain.MemoryEntry {
	return &memdomain.MemoryEntry{ID: id, Importance: importance}
}

func TestMemoryManagerGetUsesTenantFromContext(t *testing.T) {
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	ctx := WithTenantContext(context.Background(), "tenant-42")
	repo.On("Get", ctx, "tenant-42", "e1").Return(entry("e1", 0.5), nil).Once()

	got, err := m.Get(ctx, "e1")
	require.NoError(t, err)
	assert.Equal(t, "e1", got.ID)
	repo.AssertExpectations(t)
}

func TestMemoryManagerGetPropagatesError(t *testing.T) {
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("Get", mock.Anything, "", "e1").Return(nil, ErrNotFound).Once()

	_, err := m.Get(context.Background(), "e1")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryManagerGetNilRepo(t *testing.T) {
	m := NewMemoryManager(testLogger(), nil)
	_, err := m.Get(context.Background(), "e1")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryManagerAddSwallowsRepoError(t *testing.T) {
	// 极端情况：持久化失败只记录日志，不向上传播（设计意图：缓冲写入失败不阻断对话）。
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("Add", mock.Anything, mock.Anything).Return(errors.New("db down")).Once()
	assert.NoError(t, m.Add(context.Background(), &MemoryEntry{ID: "e1"}))
	repo.AssertExpectations(t)
}

func TestMemoryManagerDeleteForwardsTenantAndError(t *testing.T) {
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	ctx := WithTenantContext(context.Background(), "t1")
	repo.On("Delete", ctx, "t1", "e1").Return(assert.AnError).Once()
	assert.ErrorIs(t, m.Delete(ctx, "e1"), assert.AnError)
	repo.AssertExpectations(t)
}

func TestMemoryManagerClearNilSessionCtx(t *testing.T) {
	// 极端情况：nil session 上下文安全 no-op。
	m := NewMemoryManager(testLogger(), new(mockMemoryRepo))
	assert.NoError(t, m.Clear(context.Background(), nil))
}

func TestMemoryManagerClearForwards(t *testing.T) {
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("ClearSession", mock.Anything, "t1", "s1").Return(nil).Once()
	assert.NoError(t, m.Clear(context.Background(), &SessionContext{TenantID: "t1", SessionID: "s1"}))
	repo.AssertExpectations(t)
}

func TestMemoryManagerGetStatsNilRepoOrCtx(t *testing.T) {
	m := NewMemoryManager(testLogger(), nil)
	stats, err := m.GetStats(context.Background(), &SessionContext{TenantID: "t1"})
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	stats, err = m.GetStats(context.Background(), nil)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
}

func TestMemoryManagerGetSummaryNilRepoOrCtx(t *testing.T) {
	m := NewMemoryManager(testLogger(), nil)
	got, err := m.GetSummary(context.Background(), &SessionContext{TenantID: "t1", SessionID: "s1"})
	assert.NoError(t, err)
	assert.Equal(t, "", got)
	got, err = m.GetSummary(context.Background(), nil)
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestMemoryManagerCleanupNoop(t *testing.T) {
	assert.NoError(t, NewMemoryManager(testLogger(), nil).Cleanup(context.Background()))
}

func TestMemoryManagerSearchFiltersAndLimits(t *testing.T) {
	// 极端情况：MinScore 过滤 + Limit 截断 + Score=Importance。
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	req := &MemorySearchRequest{
		Query:    "q",
		Context:  &SessionContext{TenantID: "t1", UserID: "u1"},
		MinScore: 0.6,
		Limit:    1,
	}
	repo.On("Search", mock.Anything, "t1", "u1", "q", 1).
		Return([]*memdomain.MemoryEntry{entry("low", 0.2), entry("mid", 0.7), entry("high", 0.9)}, nil).Once()

	results, err := m.Search(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "mid", results[0].Entry.ID) // 过滤后 [mid, high]，截断到 1 → mid
	assert.Equal(t, 0.7, results[0].Score)
	repo.AssertExpectations(t)
}

func TestMemoryManagerSearchRepoErrorSwallowed(t *testing.T) {
	// 极端情况：repo 查询失败返回空结果而非错误。
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("Search", mock.Anything, "", "", "q", 0).Return(nil, assert.AnError).Once()
	results, err := m.Search(context.Background(), &MemorySearchRequest{Query: "q"})
	assert.NoError(t, err)
	assert.Empty(t, results)
}

func TestMemoryManagerSearchNilContext(t *testing.T) {
	// 极端情况：Context nil 时以空 tenant/user 查询，不 panic。
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("Search", mock.Anything, "", "", "q", 0).Return([]*memdomain.MemoryEntry{entry("e", 1)}, nil).Once()
	results, err := m.Search(context.Background(), &MemorySearchRequest{Query: "q"})
	require.NoError(t, err)
	assert.Len(t, results, 1)
	repo.AssertExpectations(t)
}

func TestMemoryManagerGetRecentMemoryDelegatesToSearch(t *testing.T) {
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	ctx := WithTenantContext(context.Background(), "t1")
	sess := &SessionContext{TenantID: "t1", SessionID: "s1"}
	repo.On("Search", mock.Anything, "t1", "", "", 2).
		Return([]*memdomain.MemoryEntry{entry("a", 0.8), entry("b", 0.3)}, nil).Once()

	entries, err := m.GetRecentMemory(ctx, sess, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "a", entries[0].ID)
	assert.Equal(t, "b", entries[1].ID)
	repo.AssertExpectations(t)
}

func TestMemoryManagerGetRecentMemoryNilSession(t *testing.T) {
	// 极端情况：nil session → Search 的 req.Context=nil → 空 tenant/user。
	repo := new(mockMemoryRepo)
	m := NewMemoryManager(testLogger(), repo)
	repo.On("Search", mock.Anything, "", "", "", 5).Return(nil, nil).Once()
	entries, err := m.GetRecentMemory(context.Background(), nil, 5)
	require.NoError(t, err)
	assert.Empty(t, entries)
	repo.AssertExpectations(t)
}
