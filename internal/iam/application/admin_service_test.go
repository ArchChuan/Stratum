package application

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// stubAdminRepo 记录调用并返回脚本化结果。
type stubAdminRepo struct {
	countErr     error
	listErr      error
	getErr       error
	createErr    error
	patchErr     error
	hardErr      error
	provisionErr error
	lastFilter   domain.TenantFilter
	provisioned  []string
}

func (r *stubAdminRepo) Count(_ context.Context, f domain.TenantFilter) (int, error) {
	r.lastFilter = f
	return 7, r.countErr
}
func (r *stubAdminRepo) List(_ context.Context, f domain.TenantFilter) ([]domain.Tenant, error) {
	r.lastFilter = f
	return []domain.Tenant{{ID: "t1"}}, r.listErr
}
func (r *stubAdminRepo) Get(_ context.Context, id string) (*domain.Tenant, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return &domain.Tenant{ID: id}, nil
}
func (r *stubAdminRepo) Create(_ context.Context, t domain.Tenant) error { return r.createErr }
func (r *stubAdminRepo) UpdatePatch(_ context.Context, id string, patch domain.TenantPatch) error {
	return r.patchErr
}
func (r *stubAdminRepo) HardDelete(_ context.Context, id string) error { return r.hardErr }
func (r *stubAdminRepo) ProvisionSchema(_ context.Context, tenantID string) error {
	r.provisioned = append(r.provisioned, tenantID)
	return r.provisionErr
}

// stubCleaner 记录 cleaner 调用。
type stubCleaner struct {
	mu      sync.Mutex
	called  []string
	dropErr error
}

func (c *stubCleaner) DropTenantSchema(_ context.Context, tenantID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = append(c.called, "schema:"+tenantID)
	return c.dropErr
}
func (c *stubCleaner) DropTenantCollections(_ context.Context, tenantID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = append(c.called, "vector:"+tenantID)
	return c.dropErr
}
func (c *stubCleaner) DropTenantObjects(_ context.Context, tenantID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = append(c.called, "object:"+tenantID)
	return c.dropErr
}
func (c *stubCleaner) Invalidate(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = append(c.called, "cache:"+tenantID)
}

func (c *stubCleaner) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.called...)
}

// adminServiceWithAll 装配全部 cleaner 的 service。
func adminServiceWithAll(repo *stubAdminRepo) (*AdminService, *stubCleaner) {
	c := &stubCleaner{}
	svc := NewAdminService(repo,
		WithSchemaCleaner(c),
		WithVectorCleaner(c),
		WithObjectCleaner(c),
		WithCacheInvalidator(c),
	)
	return svc, c
}

func TestNormaliseFilter(t *testing.T) {
	// 极端情况：page/pageSize 越界归一化。
	cases := []struct {
		name     string
		in       domain.TenantFilter
		wantPage int
		wantSize int
	}{
		{"zero page", domain.TenantFilter{Page: 0, PageSize: 10}, 1, 10},
		{"negative page", domain.TenantFilter{Page: -3, PageSize: 10}, 1, 10},
		{"zero page size", domain.TenantFilter{Page: 2, PageSize: 0}, 2, constants.DefaultPageSize},
		{"negative page size", domain.TenantFilter{Page: 2, PageSize: -1}, 2, constants.DefaultPageSize},
		{"page size too large", domain.TenantFilter{Page: 2, PageSize: constants.MaxPageSize + 1}, 2, constants.DefaultPageSize},
		{"exact max ok", domain.TenantFilter{Page: 2, PageSize: constants.MaxPageSize}, 2, constants.MaxPageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normaliseFilter(tc.in)
			assert.Equal(t, tc.wantPage, got.Page)
			assert.Equal(t, tc.wantSize, got.PageSize)
		})
	}
}

func TestListTenantsNormalisesAndPassesFilter(t *testing.T) {
	repo := &stubAdminRepo{}
	svc, _ := adminServiceWithAll(repo)
	res, err := svc.ListTenants(context.Background(), domain.TenantFilter{Page: 0, PageSize: 999, Status: "active"})
	require.NoError(t, err)
	assert.Equal(t, 7, res.Total)
	assert.Len(t, res.Tenants, 1)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, constants.DefaultPageSize, res.PageSize)
	assert.Equal(t, "active", repo.lastFilter.Status)
}

func TestListTenantsCountError(t *testing.T) {
	repo := &stubAdminRepo{countErr: errors.New("count failed")}
	svc, _ := adminServiceWithAll(repo)
	_, err := svc.ListTenants(context.Background(), domain.TenantFilter{})
	assert.Error(t, err)
}

func TestListTenantsListError(t *testing.T) {
	repo := &stubAdminRepo{listErr: errors.New("list failed")}
	svc, _ := adminServiceWithAll(repo)
	_, err := svc.ListTenants(context.Background(), domain.TenantFilter{})
	assert.Error(t, err)
}

func TestGetTenantForwards(t *testing.T) {
	repo := &stubAdminRepo{}
	svc, _ := adminServiceWithAll(repo)
	got, err := svc.GetTenant(context.Background(), "t1")
	require.NoError(t, err)
	assert.Equal(t, "t1", got.ID)
}

func TestGetTenantNotFound(t *testing.T) {
	repo := &stubAdminRepo{getErr: domain.ErrTenantNotFound}
	svc, _ := adminServiceWithAll(repo)
	_, err := svc.GetTenant(context.Background(), "ghost")
	assert.ErrorIs(t, err, domain.ErrTenantNotFound)
}

func TestCreateTenantProvisionsSchema(t *testing.T) {
	repo := &stubAdminRepo{}
	svc, _ := adminServiceWithAll(repo)
	got, err := svc.CreateTenant(context.Background(), "Acme", "acme", "pro", "active")
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, "Acme", got.Name)
	assert.Equal(t, "acme", got.Slug)
	require.Len(t, repo.provisioned, 1)
	assert.Equal(t, got.ID, repo.provisioned[0])
}

func TestCreateTenantPropagatesCreateError(t *testing.T) {
	repo := &stubAdminRepo{createErr: errors.New("insert failed")}
	svc, _ := adminServiceWithAll(repo)
	_, err := svc.CreateTenant(context.Background(), "Acme", "acme", "", "")
	assert.Error(t, err)
	assert.Empty(t, repo.provisioned, "no schema provision after failed insert")
}

func TestCreateTenantPropagatesProvisionError(t *testing.T) {
	repo := &stubAdminRepo{provisionErr: errors.New("provision failed")}
	svc, _ := adminServiceWithAll(repo)
	_, err := svc.CreateTenant(context.Background(), "Acme", "acme", "", "")
	assert.Error(t, err)
	assert.Len(t, repo.provisioned, 1)
}

func TestUpdateTenantForwards(t *testing.T) {
	repo := &stubAdminRepo{}
	svc, _ := adminServiceWithAll(repo)
	err := svc.UpdateTenant(context.Background(), "t1", domain.TenantPatch{Plan: "pro"})
	assert.NoError(t, err)
}

func TestDeleteTenantRunsAllCleaners(t *testing.T) {
	// 成功路径：先删行，再按 vector → object → schema → cache 顺序清理。
	repo := &stubAdminRepo{}
	svc, c := adminServiceWithAll(repo)
	err := svc.DeleteTenant(context.Background(), "t1")
	assert.NoError(t, err)
	assert.Equal(t, []string{"vector:t1", "object:t1", "schema:t1", "cache:t1"}, c.snapshot())
}

func TestDeleteTenantCleanerErrorsAreWarned(t *testing.T) {
	// 极端情况：cleaner 失败只记录日志，不阻断删除结果。
	repo := &stubAdminRepo{}
	bad := &stubCleaner{dropErr: errors.New("drop failed")}
	svc := NewAdminService(repo,
		WithSchemaCleaner(bad), WithVectorCleaner(bad), WithObjectCleaner(bad), WithCacheInvalidator(bad))
	err := svc.DeleteTenant(context.Background(), "t1")
	assert.NoError(t, err)
	assert.Len(t, bad.snapshot(), 4, "all cleaners must run despite errors")
}

func TestDeleteTenantHardDeleteErrorStops(t *testing.T) {
	// 极端情况：行删除失败时任何 cleaner 都不应运行。
	repo := &stubAdminRepo{hardErr: errors.New("delete failed")}
	svc, c := adminServiceWithAll(repo)
	err := svc.DeleteTenant(context.Background(), "t1")
	assert.Error(t, err)
	assert.Empty(t, c.snapshot(), "cleaners must not run after hard delete failure")
}

func TestAdminServiceOptionsNilLoggerSafe(t *testing.T) {
	// 极端情况：不传 logger/cleaner 时使用默认 no-op logger，删除仍成功。
	repo := &stubAdminRepo{}
	svc := NewAdminService(repo)
	err := svc.DeleteTenant(context.Background(), "t1")
	assert.NoError(t, err)
}
