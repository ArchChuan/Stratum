package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestEvaluationHandlerEnqueueRunReturnsAcceptedJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeEvaluationJobs{}
	h := NewEvaluationHandler(nil, jobs, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.POST("/evaluations/runs", withTenant("tenant-1"), h.EnqueueRun)

	req := httptest.NewRequest(http.MethodPost, "/evaluations/runs", strings.NewReader(`{
		"resource":{"kind":"skill","resource_id":"skill-1","revision_id":"version-2"},
		"suite_revision_id":"suite-revision-1","idempotency_key":"request-1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"job_id":"job-1"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if jobs.tenantID != "tenant-1" {
		t.Fatalf("tenant not propagated: %q", jobs.tenantID)
	}
}

func TestEvaluationHandlerGenerateOptimizationReturnsCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	optimization := &fakeOptimizationService{}
	h := NewEvaluationHandler(nil, &fakeEvaluationJobs{}, nil, optimization, nil, zap.NewNop())
	r := gin.New()
	r.POST("/evaluations/optimizations", withTenant("tenant-1"), h.GenerateOptimization)
	req := httptest.NewRequest(http.MethodPost, "/evaluations/optimizations", strings.NewReader(`{
		"baseline":{"kind":"skill","resource_id":"skill-1","revision_id":"version-1"},
		"suite_revision_id":"suite-revision-1","search_space":{"temperature":[0.1,0.2]}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"revision_id":"candidate-1"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func withTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), tenantID))
		c.Next()
	}
}

type fakeEvaluationJobs struct{ tenantID string }

func (f *fakeEvaluationJobs) EnqueueRun(_ context.Context, tenantID string, _ application.EnqueueRunInput) (domain.EvaluationJob, error) {
	f.tenantID = tenantID
	return domain.EvaluationJob{ID: "job-1", Status: domain.JobQueued}, nil
}

func (f *fakeEvaluationJobs) Get(_ context.Context, _ string, _ string) (domain.EvaluationJob, error) {
	return domain.EvaluationJob{ID: "job-1", Status: domain.JobQueued}, nil
}

type fakeOptimizationService struct{}

func (f *fakeOptimizationService) Generate(
	_ context.Context, _ string, input application.GenerateCandidatesInput,
) (domain.OptimizationJob, []domain.OptimizationCandidate, error) {
	job := domain.OptimizationJob{ID: "optimization-1", Baseline: input.Baseline, Status: domain.JobSucceeded}
	return job, []domain.OptimizationCandidate{{
		ID: "candidate-record-1", OptimizationJobID: job.ID,
		Revision: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "candidate-1"},
	}}, nil
}
