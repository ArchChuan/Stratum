package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func setupAdminRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/tenants", h.ListTenants)
	r.POST("/admin/tenants", h.CreateTenant)
	r.DELETE("/admin/tenants/:id", h.DeleteTenant)
	return r
}

func TestListTenants_noFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	now := time.Now()
	mock.ExpectQuery("SELECT id, name, slug, plan, status, created_at FROM").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "slug", "plan", "status", "created_at"}).
			AddRow("tid1", "Acme", "acme", "pro", "active", now))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

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
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateTenant_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("INSERT INTO public.tenants").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	body := `{"name":"Acme","slug":"acme","plan":"pro","status":"active"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestDeleteTenant_softDelete(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("UPDATE public.tenants SET deleted_at").
		WithArgs(pgxmock.AnyArg(), "tid1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/tid1", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestDeleteTenant_notFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("UPDATE public.tenants SET deleted_at").
		WithArgs(pgxmock.AnyArg(), "nonexistent").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/nonexistent", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
