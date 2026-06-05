package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func injectTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "user-1", Role: tenantdb.RoleTenantAdmin}
		c.Request = c.Request.WithContext(tenantdb.WithTenant(c.Request.Context(), tc))
		c.Next()
	}
}

func setupTenantHandlerRouter(h *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	inject := injectTenant("tenant-abc")
	injectAdmin := func(c *gin.Context) { c.Set("auth.role", "admin"); c.Next() }
	r.GET("/tenant/members", inject, h.ListMembers)
	r.POST("/tenant/members/invite", inject, injectAdmin, h.InviteMember)
	r.DELETE("/tenant/members/:user_id", inject, h.RemoveMember)
	return r
}

func TestListMembers_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery("SELECT COUNT").
		WithArgs("tenant-abc").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	now := time.Now()
	mock.ExpectQuery("SELECT tm.user_id").
		WithArgs("tenant-abc", 20, 0).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "github_login", "avatar_url", "role", "joined_at"}).
			AddRow("user-1", "alice", "https://avatars.githubusercontent.com/alice", "admin", now))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
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
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestInviteMember_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("INSERT INTO public.invitations").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
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
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestInviteMember_invalidEmail(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
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
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("DELETE FROM public.tenant_members").
		WithArgs("tenant-abc", "user-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/user-1", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestRemoveMember_notFound(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectExec("DELETE FROM public.tenant_members").
		WithArgs("tenant-abc", "ghost-user").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantHandlerRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/ghost-user", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
