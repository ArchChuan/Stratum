package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type stubObservationQueryService struct {
	items []domain.EvalObservation
	err   error
}

func (s *stubObservationQueryService) ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
	from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.items, nil
}

func (s *stubObservationQueryService) GetObservation(ctx context.Context, tenantID, id string,
) (*domain.EvalObservation, error) {
	if len(s.items) > 0 {
		return &s.items[0], nil
	}
	return nil, nil
}

func TestListObservationsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	h.WithObservationService(&stubObservationQueryService{items: []domain.EvalObservation{
		{ID: "obs-1", TraceID: "trace-1", Resource: domain.ObservationResourceRef{
			Kind: "agent", ResourceID: "agent-1",
		}, Verdict: domain.VerdictPass},
	}})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/evaluations/observations", withTenant("tenant-1"), h.ListObservations)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/evaluations/observations?resource_kind=agent&resource_id=agent-1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Items []domain.EvalObservation `json:"items"`
		Total int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Items) != 1 || body.Total != 1 {
		t.Fatalf("body mismatch: %s", rec.Body.String())
	}
}

func TestGetObservationHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	h.WithObservationService(&stubObservationQueryService{items: []domain.EvalObservation{
		{ID: "obs-1", Verdict: domain.VerdictPass},
	}})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/evaluations/observations/:id", withTenant("tenant-1"), h.GetObservation)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/observations/obs-1", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got domain.EvalObservation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.ID != "obs-1" {
		t.Fatalf("id = %q, want obs-1", got.ID)
	}
}

func TestGetObservationHandlerNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	h.WithObservationService(&stubObservationQueryService{})
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/evaluations/observations/:id", withTenant("tenant-1"), h.GetObservation)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/observations/obs-missing", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListObservationsHandlerServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/evaluations/observations", withTenant("tenant-1"), h.ListObservations)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/observations", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
