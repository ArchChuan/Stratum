package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakeTenantRepo is an in-memory port.TenantRepo for handler tests.
type fakeTenantRepo struct {
	count          int
	members        []domain.Member
	listLimit      int
	listOffset     int
	memberRoles    map[string]string
	deleteErr      error
	deleted        []string
	tenantName     string
	tenantSettings []byte
}

func (f *fakeTenantRepo) CountMembers(_ context.Context, _ string) (int, error) {
	return f.count, nil
}

func (f *fakeTenantRepo) ListMembers(_ context.Context, _ string, limit, offset int) ([]domain.Member, error) {
	f.listLimit = limit
	f.listOffset = offset
	return f.members, nil
}

func (f *fakeTenantRepo) GetMemberRole(_ context.Context, _, userID string) (string, error) {
	if r, ok := f.memberRoles[userID]; ok {
		return r, nil
	}
	return "", domain.ErrMemberNotFound
}

func (f *fakeTenantRepo) UpdateMemberRole(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeTenantRepo) DeleteMember(_ context.Context, _, userID string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.memberRoles[userID]; !ok {
		return domain.ErrMemberNotFound
	}
	f.deleted = append(f.deleted, userID)
	return nil
}

func (f *fakeTenantRepo) GetTenantSettings(_ context.Context, _ string) (string, bool, []byte, error) {
	return f.tenantName, false, f.tenantSettings, nil
}

func (f *fakeTenantRepo) UpdateTenantName(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeTenantRepo) UpdateTenantSettings(_ context.Context, _ string, b []byte) error {
	f.tenantSettings = b
	return nil
}

func (f *fakeTenantRepo) ListUserTenants(_ context.Context, _ string) ([]domain.UserTenantInfo, error) {
	return nil, nil
}

func injectTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "user-1", Role: tenantdb.RoleTenantAdmin}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		ctx = reqctx.WithTenantID(ctx, tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func newTenantHandler(repo *fakeTenantRepo) *TenantHandler {
	svc := application.NewTenantService(repo, zap.NewNop(), [32]byte{}, nil)
	return NewTenantHandler(svc, nil, zap.NewNop())
}

func setupTenantHandlerRouter(h *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	inject := injectTenant("tenant-abc")
	injectAdmin := func(c *gin.Context) { c.Set("auth.role", "admin"); c.Set("auth.sub", "user-1"); c.Next() }
	r.GET("/tenant/members", inject, h.ListMembers)
	r.DELETE("/tenant/members/:user_id", inject, injectAdmin, h.RemoveMember)
	return r
}

func TestListMembers_success(t *testing.T) {
	now := time.Now()
	repo := &fakeTenantRepo{
		count: 1,
		members: []domain.Member{
			{UserID: "user-1", GitHubLogin: "alice", AvatarURL: "https://avatars.githubusercontent.com/alice", Role: "admin", JoinedAt: now},
		},
	}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/members", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	members, _ := resp["members"].([]interface{})
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
}

func TestListMembers_appliesPaginationQuery(t *testing.T) {
	repo := &fakeTenantRepo{count: 25, members: []domain.Member{}}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/members?page=2&page_size=10", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if repo.listLimit != 10 || repo.listOffset != 10 {
		t.Fatalf("expected limit=10 offset=10, got limit=%d offset=%d", repo.listLimit, repo.listOffset)
	}

	var resp dto.ListMembersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 25 || resp.Page != 2 || resp.PageSize != 10 {
		t.Fatalf("unexpected pagination metadata: %+v", resp)
	}
}

func TestRemoveMember_success(t *testing.T) {
	repo := &fakeTenantRepo{
		memberRoles: map[string]string{"user-2": "member"},
	}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/user-2", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "user-2" {
		t.Errorf("expected user-2 deleted, got %v", repo.deleted)
	}
}

func TestRemoveMember_notFound(t *testing.T) {
	repo := &fakeTenantRepo{
		memberRoles: map[string]string{},
		deleteErr:   errors.New("never reached"),
	}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/ghost-user", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteSelfReadsJWTContextRole(t *testing.T) {
	h := newTenantHandler(&fakeTenantRepo{})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.DELETE("/tenant", injectTenant("tenant-abc"), func(c *gin.Context) {
		c.Set(middleware.ContextKeyRole, "owner")
		c.Next()
	}, h.DeleteSelf)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/tenant", nil)) //nolint:noctx
	if w.Code == http.StatusForbidden {
		t.Fatalf("owner role from JWT context was ignored: %s", w.Body.String())
	}
}
