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
	getByIDFn func(ctx context.Context, id string) (*domain.AuditEvent, error)
}

func (f *fakeAuditQueryService) Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, filter)
	}
	return nil, nil
}

func (f *fakeAuditQueryService) GetByID(ctx context.Context, id string) (*domain.AuditEvent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
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
}

func TestAuditHandler_ListEvents_WithResults(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			return []domain.AuditEvent{
				{ID: "evt-1", Action: "POST /test"},
			}, nil
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
}

func TestAuditHandler_ListEvents_RepoError(t *testing.T) {
	q := &fakeAuditQueryService{
		queryFn: func(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
			return nil, errors.New("db down")
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
		getByIDFn: func(_ context.Context, id string) (*domain.AuditEvent, error) {
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
		getByIDFn: func(_ context.Context, id string) (*domain.AuditEvent, error) {
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
		getByIDFn: func(_ context.Context, id string) (*domain.AuditEvent, error) {
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
