package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakeDiagnosticsService mocks DiagnosticsService for tests.
type fakeDiagnosticsService struct {
	diagnostics *application.Diagnostics
	diagErr     error
}

func (f *fakeDiagnosticsService) GetDiagnostics(_ context.Context, _ string) (*application.Diagnostics, error) {
	if f.diagErr != nil {
		return nil, f.diagErr
	}
	return f.diagnostics, nil
}

// fakeMemoryService mocks MemoryService for tests.
type fakeMemoryService struct {
	forgetErr error
}

func (f *fakeMemoryService) ForgetMemory(_ context.Context, _ *application.ForgetMemoryRequest) error {
	return f.forgetErr
}

func newAdminMemoryHandler(diagSvc *fakeDiagnosticsService, memSvc *fakeMemoryService) *AdminMemoryHandler {
	return NewAdminMemoryHandler(diagSvc, memSvc, zap.NewNop())
}

func setupAdminMemoryRouter(h *AdminMemoryHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))

	injectSystemAdmin := func(c *gin.Context) {
		c.Set(middleware.ContextKeySystemRole, string(domain.SystemRoleSystemAdmin))
		c.Next()
	}

	r.GET("/api/admin/memory/diagnostics", injectSystemAdmin, h.GetDiagnostics)
	r.POST("/api/admin/memory/facts/:id/forget", injectSystemAdmin, h.ForgetFact)
	r.GET("/api/admin/memory/tenants", injectSystemAdmin, h.ListTenants)
	return r
}

func TestGetDiagnostics_success(t *testing.T) {
	diagSvc := &fakeDiagnosticsService{
		diagnostics: &application.Diagnostics{
			ActiveFactCount: 42,
			SupersededCount: 8,
			QueueLag:        3,
			TopEntities: []application.EntityCount{
				{Name: "user-123", Count: 15},
			},
			FrecencyHistogram: []int{5, 10, 15, 8, 4},
		},
	}
	h := newAdminMemoryHandler(diagSvc, nil)
	r := setupAdminMemoryRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/admin/memory/diagnostics?tenant_id=tenant-abc", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp application.Diagnostics
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ActiveFactCount != 42 {
		t.Errorf("expected ActiveFactCount=42, got %d", resp.ActiveFactCount)
	}
	if len(resp.TopEntities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(resp.TopEntities))
	}
}

func TestGetDiagnostics_missingTenantID(t *testing.T) {
	h := newAdminMemoryHandler(&fakeDiagnosticsService{}, nil)
	r := setupAdminMemoryRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/admin/memory/diagnostics", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestForgetFact_success(t *testing.T) {
	memSvc := &fakeMemoryService{}
	h := newAdminMemoryHandler(nil, memSvc)
	r := setupAdminMemoryRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/memory/facts/fact-123/forget?tenant_id=tenant-abc&user_id=user-456", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["message"] != "fact forgotten" {
		t.Errorf("expected message='fact forgotten', got %v", resp["message"])
	}
}

func TestForgetFact_missingParams(t *testing.T) {
	h := newAdminMemoryHandler(nil, &fakeMemoryService{})
	r := setupAdminMemoryRouter(h)

	tests := []struct {
		name string
		url  string
	}{
		{"missing tenant_id", "/api/admin/memory/facts/fact-123/forget?user_id=user-456"},
		{"missing user_id", "/api/admin/memory/facts/fact-123/forget?tenant_id=tenant-abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, tt.url, nil) //nolint:noctx
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestListTenants_stub(t *testing.T) {
	h := newAdminMemoryHandler(nil, nil)
	r := setupAdminMemoryRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/admin/memory/tenants", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	tenants, ok := resp["tenants"].([]interface{})
	if !ok || len(tenants) != 0 {
		t.Errorf("expected empty tenants array, got %v", resp["tenants"])
	}
}
