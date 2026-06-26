package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
)

// fakeAdminRepo implements port.AdminTenantRepo for handler tests. Each method
// is wired through a function field so individual tests can override behaviour.
type fakeAdminRepo struct {
	countFn       func(context.Context, iamdomain.TenantFilter) (int, error)
	listFn        func(context.Context, iamdomain.TenantFilter) ([]iamdomain.Tenant, error)
	getFn         func(context.Context, string) (*iamdomain.Tenant, error)
	createFn      func(context.Context, iamdomain.Tenant) error
	updateFn      func(context.Context, string, iamdomain.TenantPatch) error
	deleteFn      func(context.Context, string) error
	provisionFn   func(context.Context, string) error
	provisionCall int
}

func (f *fakeAdminRepo) Count(ctx context.Context, filter iamdomain.TenantFilter) (int, error) {
	return f.countFn(ctx, filter)
}

func (f *fakeAdminRepo) List(ctx context.Context, filter iamdomain.TenantFilter) ([]iamdomain.Tenant, error) {
	return f.listFn(ctx, filter)
}

func (f *fakeAdminRepo) Get(ctx context.Context, id string) (*iamdomain.Tenant, error) {
	return f.getFn(ctx, id)
}

func (f *fakeAdminRepo) Create(ctx context.Context, t iamdomain.Tenant) error {
	return f.createFn(ctx, t)
}

func (f *fakeAdminRepo) UpdatePatch(ctx context.Context, id string, patch iamdomain.TenantPatch) error {
	return f.updateFn(ctx, id, patch)
}

func (f *fakeAdminRepo) HardDelete(ctx context.Context, id string) error {
	return f.deleteFn(ctx, id)
}

func (f *fakeAdminRepo) ProvisionSchema(ctx context.Context, tenantID string) error {
	f.provisionCall++
	if f.provisionFn == nil {
		return nil
	}
	return f.provisionFn(ctx, tenantID)
}

func setupAdminRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/admin/tenants", h.ListTenants)
	r.POST("/admin/tenants", h.CreateTenant)
	r.DELETE("/admin/tenants/:id", h.DeleteTenant)
	return r
}

func newTestAdminHandler(repo *fakeAdminRepo) *AdminHandler {
	return NewAdminHandler(iamapp.NewAdminService(repo), zap.NewNop())
}

func TestListTenants_noFilter(t *testing.T) {
	now := time.Now()
	repo := &fakeAdminRepo{
		countFn: func(_ context.Context, _ iamdomain.TenantFilter) (int, error) { return 1, nil },
		listFn: func(_ context.Context, _ iamdomain.TenantFilter) ([]iamdomain.Tenant, error) {
			return []iamdomain.Tenant{{
				ID: "tid1", Name: "Acme", Slug: "acme", Plan: "pro", Status: "active", CreatedAt: now,
			}}, nil
		},
	}
	r := setupAdminRouter(newTestAdminHandler(repo))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/tenants", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	tenants, _ := resp["tenants"].([]interface{})
	if len(tenants) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(tenants))
	}
}

func TestCreateTenant_success(t *testing.T) {
	repo := &fakeAdminRepo{
		createFn: func(_ context.Context, _ iamdomain.Tenant) error { return nil },
	}
	r := setupAdminRouter(newTestAdminHandler(repo))

	body := `{"name":"Acme","slug":"acme","plan":"pro","status":"active"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if repo.provisionCall != 1 {
		t.Fatalf("expected ProvisionSchema to be called once, got %d", repo.provisionCall)
	}
}

func TestDeleteTenant_softDelete(t *testing.T) {
	called := ""
	repo := &fakeAdminRepo{
		deleteFn: func(_ context.Context, id string) error { called = id; return nil },
	}
	r := setupAdminRouter(newTestAdminHandler(repo))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/tid1", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if called != "tid1" {
		t.Fatalf("expected HardDelete to receive tid1, got %q", called)
	}
}

func TestDeleteTenant_notFound(t *testing.T) {
	repo := &fakeAdminRepo{
		deleteFn: func(_ context.Context, _ string) error { return iamdomain.ErrTenantNotFound },
	}
	r := setupAdminRouter(newTestAdminHandler(repo))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/nonexistent", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
