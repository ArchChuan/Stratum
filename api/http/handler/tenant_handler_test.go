package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	memberRoles    map[string]string
	deleteErr      error
	deleted        []string
	createInvErr   error
	invitations    []domain.Invitation
	tenantName     string
	tenantSettings []byte
}

func (f *fakeTenantRepo) CountMembers(_ context.Context, _ string) (int, error) {
	return f.count, nil
}

func (f *fakeTenantRepo) ListMembers(_ context.Context, _ string, _, _ int) ([]domain.Member, error) {
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

func (f *fakeTenantRepo) CreateInvitation(_ context.Context, inv domain.Invitation) error {
	if f.createInvErr != nil {
		return f.createInvErr
	}
	f.invitations = append(f.invitations, inv)
	return nil
}

func (f *fakeTenantRepo) GetTenantSettings(_ context.Context, _ string) (string, []byte, error) {
	return f.tenantName, f.tenantSettings, nil
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
	svc := application.NewTenantService(repo, zap.NewNop(), "http://localhost:3000", [32]byte{}, nil)
	return NewTenantHandler(svc, zap.NewNop())
}

func setupTenantHandlerRouter(h *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	inject := injectTenant("tenant-abc")
	injectAdmin := func(c *gin.Context) { c.Set("auth.role", "admin"); c.Set("auth.sub", "user-1"); c.Next() }
	r.GET("/tenant/members", inject, h.ListMembers)
	r.POST("/tenant/members/invite", inject, injectAdmin, h.InviteMember)
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

func TestInviteMember_success(t *testing.T) {
	repo := &fakeTenantRepo{}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	body := `{"email":"bob@example.com","role":"member"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/members/invite", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	url, _ := resp["invitation_url"].(string)
	if !strings.HasPrefix(url, "http://localhost:3000/onboarding?invitation=") {
		t.Errorf("unexpected invitation_url: %s", url)
	}
	if len(repo.invitations) != 1 {
		t.Errorf("expected 1 invitation persisted, got %d", len(repo.invitations))
	}
}

func TestInviteMember_invalidEmail(t *testing.T) {
	repo := &fakeTenantRepo{}
	h := newTenantHandler(repo)
	r := setupTenantHandlerRouter(h)

	body := `{"email":"not-an-email","role":"member"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/members/invite", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
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
