package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeAuditQueryService struct {
	queryFn   func(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error)
	countFn   func(ctx context.Context, filter domain.AuditFilter) (int, error)
	getByIDFn func(ctx context.Context, tenantID, id string) (*domain.AuditEvent, error)
}

func (f *fakeAuditQueryService) Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, filter)
	}
	return nil, nil
}

func (f *fakeAuditQueryService) Count(ctx context.Context, filter domain.AuditFilter) (int, error) {
	if f.countFn != nil {
		return f.countFn(ctx, filter)
	}
	return 0, nil
}

func (f *fakeAuditQueryService) GetByID(ctx context.Context, tenantID, id string) (*domain.AuditEvent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, tenantID, id)
	}
	return nil, nil
}

func setupAuditHandlerRouter(q auditport.AuditQueryService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Set("auth.tenant_id", "t1")
		c.Next()
	})
	h := handler.NewAuditHandler(q, zap.NewNop())
	audit := r.Group("/audit")
	{
		audit.GET("/events", h.ListEvents)
		audit.GET("/events/:id", h.GetEvent)
	}
	return r
}

func TestAuditHandler_ListEvents_EmptyResult(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			return []domain.AuditEvent{}, nil
		},
		countFn: func(_ context.Context, _ domain.AuditFilter) (int, error) {
			return 0, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["events"]; !ok {
		t.Error("missing events field")
	}
	if total, ok := body["total"].(float64); !ok || total != 0 {
		t.Errorf("total=%#v, want 0", body["total"])
	}
}

func TestAuditHandler_ListEvents_WithResults(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			return []domain.AuditEvent{
				{ID: "evt-1", Action: "POST /test"},
			}, nil
		},
		countFn: func(_ context.Context, _ domain.AuditFilter) (int, error) {
			return 2, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)
	events, ok := body["events"].([]interface{})
	if !ok || len(events) != 1 {
		t.Fatalf("expected 1 event, got %#v", body["events"])
	}
	if total, ok := body["total"].(float64); !ok || total != 2 {
		t.Errorf("total=%#v, want 2 (pagination total)", body["total"])
	}
}

func TestAuditHandler_ListEvents_Pagination(t *testing.T) {
	var gotFilter domain.AuditFilter
	countCalls := 0
	queryCalls := 0
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error) {
			queryCalls++
			gotFilter = f
			return nil, nil
		},
		countFn: func(_ context.Context, f domain.AuditFilter) (int, error) {
			countCalls++
			gotFilter = f
			return 0, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events?page=2&page_size=20", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if countCalls != 1 || queryCalls != 1 {
		t.Fatalf("countCalls=%d queryCalls=%d, want 1/1", countCalls, queryCalls)
	}
	if gotFilter.Limit != 20 {
		t.Errorf("limit=%d, want 20 from page_size", gotFilter.Limit)
	}
	if gotFilter.Offset != 20 {
		t.Errorf("offset=%d, want 20 = (page-1)*page_size", gotFilter.Offset)
	}
	if gotFilter.TenantID != "t1" {
		t.Errorf("tenantID=%q, want t1", gotFilter.TenantID)
	}
}

func TestAuditHandler_ListEvents_CountError(t *testing.T) {
	q := &fakeAuditQueryService{
		countFn: func(_ context.Context, _ domain.AuditFilter) (int, error) {
			return 0, errors.New("db down")
		},
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			t.Error("Query must not be called when Count fails")
			return nil, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500 (fail closed on count error)", w.Code)
	}
}

func TestAuditHandler_ListEvents_RepoError(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			return nil, errors.New("db down")
		},
		countFn: func(_ context.Context, _ domain.AuditFilter) (int, error) {
			return 0, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", w.Code)
	}
}

func TestAuditHandler_GetEvent_Found(t *testing.T) {
	q := &fakeAuditQueryService{
		getByIDFn: func(_ context.Context, tenantID, id string) (*domain.AuditEvent, error) {
			if tenantID != "t1" {
				t.Errorf("tenantID=%q, want t1 (caller tenant must scope the read)", tenantID)
			}
			return &domain.AuditEvent{ID: id, Action: "POST /test"}, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events/evt-1", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var evt domain.AuditEvent
	if err := json.NewDecoder(w.Body).Decode(&evt); err != nil {
		t.Fatal(err)
	}
	if evt.ID != "evt-1" {
		t.Errorf("id=%q, want evt-1", evt.ID)
	}
}

func TestAuditHandler_GetEvent_NotFound(t *testing.T) {
	q := &fakeAuditQueryService{
		getByIDFn: func(_ context.Context, _, id string) (*domain.AuditEvent, error) {
			return nil, nil
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events/missing", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}

func TestAuditHandler_GetEvent_RepoError(t *testing.T) {
	q := &fakeAuditQueryService{
		getByIDFn: func(_ context.Context, _, id string) (*domain.AuditEvent, error) {
			return nil, errors.New("db down")
		},
	}
	r := setupAuditHandlerRouter(q)

	req := httptest.NewRequest(http.MethodGet, "/audit/events/evt-1", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", w.Code)
	}
}

// setupAuditRouterWithoutTenant 不写 auth.tenant_id（模拟 middleware 缺 key）：
// handler 必须 fail closed 返回 401，而不是对 type assertion panic。
func setupAuditRouterWithoutTenant(q auditport.AuditQueryService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	h := handler.NewAuditHandler(q, zap.NewNop())
	audit := r.Group("/audit")
	{
		audit.GET("/events", h.ListEvents)
		audit.GET("/events/:id", h.GetEvent)
	}
	return r
}

func TestAuditHandler_MissingTenant_FailsClosed(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			t.Error("Query must not be called without a tenant")
			return nil, nil
		},
		getByIDFn: func(_ context.Context, _, _ string) (*domain.AuditEvent, error) {
			t.Error("GetByID must not be called without a tenant")
			return nil, nil
		},
	}
	r := setupAuditRouterWithoutTenant(q)

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "list", path: "/audit/events"},
		{name: "get", path: "/audit/events/evt-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil) //nolint:noctx
			w := httptest.NewRecorder()
			// 断言不 panic：旧实现对 tid.(string) 直接断言，缺 key 时 panic。
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, want 401 (fail closed)", w.Code)
			}
		})
	}
}
