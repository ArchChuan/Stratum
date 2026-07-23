package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestEvaluationHandlerEnqueueRunReturnsAcceptedJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeEvaluationJobs{}
	h := NewEvaluationHandler(nil, jobs, nil, nil, nil, nil, nil, nil, zap.NewNop())
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

func TestEvaluationHandlerCreateBaselineUsesTenantAndResourcePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baselines := &fakeEvaluationBaselines{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithBaselineService(baselines)
	r := gin.New()
	r.POST("/evaluations/resources/:kind/:id/baseline", withTenant("tenant-1"), h.CreateBaseline)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/evaluations/resources/agent/agent-1/baseline", nil))

	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"revision_id":"revision-1"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if baselines.tenantID != "tenant-1" || baselines.kind != domain.ResourceKindAgent ||
		baselines.resourceID != "agent-1" {
		t.Fatalf("baseline path not propagated: %+v", baselines)
	}
}

func TestEvaluationHandlerGenerateOptimizationReturnsCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	optimization := &fakeOptimizationService{}
	h := NewEvaluationHandler(nil, &fakeEvaluationJobs{}, nil, optimization, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.POST("/evaluations/optimizations", withTenant("tenant-1"), h.GenerateOptimization)
	req := httptest.NewRequest(http.MethodPost, "/evaluations/optimizations", strings.NewReader(`{
		"baseline":{"kind":"skill","resource_id":"skill-1","revision_id":"version-1"},
		"suite_revision_id":"suite-revision-1","search_space":{"temperature":[0.1,0.2]},
		"idempotency_key":"request-1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"revision_id":"candidate-1"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if optimization.input.IdempotencyKey != "request-1" {
		t.Fatalf("idempotency key not propagated: %+v", optimization.input)
	}
}

func TestEvaluationHandlerGenerateOptimizationAcceptsLegacyRequestWithoutIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	optimization := &fakeOptimizationService{}
	h := NewEvaluationHandler(nil, &fakeEvaluationJobs{}, nil, optimization, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.POST("/evaluations/optimizations", withTenant("tenant-1"), h.GenerateOptimization)
	req := httptest.NewRequest(http.MethodPost, "/evaluations/optimizations", strings.NewReader(`{
		"baseline":{"kind":"skill","resource_id":"skill-1","revision_id":"version-1"},
		"suite_revision_id":"suite-revision-1","search_space":{}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if optimization.input.IdempotencyKey != "" {
		t.Fatalf("legacy request should preserve empty key for application fallback: %+v", optimization.input)
	}
}

func TestEvaluationHandlerGenerateOptimizationUsesHeaderAndMapsConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	optimization := &fakeOptimizationService{err: domain.ErrOptimizationIdempotencyConflict}
	h := NewEvaluationHandler(nil, &fakeEvaluationJobs{}, nil, optimization, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/optimizations", withTenant("tenant-1"), h.GenerateOptimization)
	req := httptest.NewRequest(http.MethodPost, "/evaluations/optimizations", strings.NewReader(`{
		"baseline":{"kind":"skill","resource_id":"skill-1","revision_id":"version-1"},
		"suite_revision_id":"suite-revision-1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "header-key")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || rec.Body.String() != `{"error":"optimization idempotency conflict"}` {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if optimization.input.IdempotencyKey != "header-key" {
		t.Fatalf("header key not propagated: %+v", optimization.input)
	}
}

func TestEvaluationHandlerListResourcesPropagatesFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queries := &fakeEvaluationQueries{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, queries, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/evaluations/resources", withTenant("tenant-1"), h.ListResources)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/evaluations/resources?resource_kind=skill&resource_id=skill-1&status=published&cursor=cursor-1&limit=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if queries.tenantID != "tenant-1" || queries.filter != (port.CenterFilter{
		ResourceKind: "skill", ResourceID: "skill-1", Status: "published", Cursor: "cursor-1", Limit: 7,
	}) {
		t.Fatalf("query not propagated: tenant=%q filter=%+v", queries.tenantID, queries.filter)
	}
	var page domain.ResourcePage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("typed page response=%s err=%v", rec.Body.String(), err)
	}
}

func TestEvaluationHandlerListExperimentsSerializesSafePromotionEvidence(t *testing.T) {
	queries := &fakeEvaluationQueries{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, queries, nil, zap.NewNop())
	r := gin.New()
	r.GET("/evaluations/experiments", withTenant("tenant-1"), h.ListExperiments)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/experiments", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"eligible":true`) ||
		strings.Contains(rec.Body.String(), "decision_snapshot") || strings.Contains(rec.Body.String(), `"metrics"`) {
		t.Fatalf("unsafe or incomplete experiment response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvaluationHandlerRejectCandidateDerivesActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	candidates := &fakeCandidateCommands{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, candidates, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/candidates/:id/reject", withTenantAndUser("tenant-1", "user-1"), h.RejectCandidate)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluations/candidates/candidate-1/reject", strings.NewReader(
		`{"reason":"unsafe","idempotency_key":"request-1","expected_state_version":1,"actor_id":"attacker"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if candidates.tenantID != "tenant-1" || candidates.candidateID != "candidate-1" ||
		candidates.input.ActorID != "user-1" || candidates.input.Reason != "unsafe" ||
		candidates.input.IdempotencyKey != "request-1" || candidates.input.ExpectedStateVersion != 1 {
		t.Fatalf("command not propagated safely: %+v", candidates)
	}
}

func TestEvaluationHandlerExperimentCommandValidationUsesFrozenEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, &fakeExperimentCommands{}, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/experiments/:id/pause", withTenantAndUser("tenant-1", "user-1"), h.PauseExperiment)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluations/experiments/experiment-1/pause", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Body.String(), `{"error":`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvaluationHandlerEvaluateExperimentLegacyBodyUsesStableIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	experiments := &fakeExperimentCommands{}
	h := NewEvaluationHandler(nil, nil, nil, nil, experiments, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/experiments/:id/evaluate", withTenant("tenant-1"), h.EvaluateExperiment)
	body := `{"samples":10,"quality_improvement":0.2}`
	for range 2 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/evaluations/experiments/experiment-1/evaluate", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("legacy request status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if len(experiments.evaluateKeys) != 2 || experiments.evaluateKeys[0] == "" ||
		experiments.evaluateKeys[0] != experiments.evaluateKeys[1] {
		t.Fatalf("unstable legacy idempotency keys: %v", experiments.evaluateKeys)
	}
}

func TestEvaluationHandlerListSuitesPropagatesResourceID(t *testing.T) {
	queries := &fakeEvaluationQueries{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, queries, nil, zap.NewNop())
	r := gin.New()
	r.GET("/evaluations/suites", withTenant("tenant-1"), h.ListSuites)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/suites?resource_id=skill-1", nil))
	if rec.Code != http.StatusOK || queries.filter.ResourceID != "skill-1" {
		t.Fatalf("status=%d filter=%+v body=%s", rec.Code, queries.filter, rec.Body.String())
	}
}

func TestEvaluationHandlerCandidateResponseContainsOnlySafeDiff(t *testing.T) {
	queries := &fakeEvaluationQueries{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, queries, nil, zap.NewNop())
	r := gin.New()
	r.GET("/evaluations/candidates", withTenant("tenant-1"), h.ListCandidates)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/candidates", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"before":"old","after":"new"`) ||
		strings.Contains(rec.Body.String(), "raw_payload") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func withTenantAndUser(tenantID, userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), tenantID))
		c.Set(middleware.ContextKeySub, userID)
		c.Next()
	}
}

type fakeEvaluationQueries struct {
	tenantID string
	filter   port.CenterFilter
}

func (f *fakeEvaluationQueries) Overview(context.Context, string) (domain.CenterOverview, error) {
	return domain.CenterOverview{}, nil
}
func (f *fakeEvaluationQueries) ListResources(_ context.Context, tenantID string, filter port.CenterFilter) (domain.ResourcePage, error) {
	f.tenantID, f.filter = tenantID, filter
	return domain.ResourcePage{Items: []domain.ResourceSummary{{ID: "revision-1"}}}, nil
}

func (f *fakeEvaluationQueries) ListSuites(_ context.Context, tenantID string, filter port.CenterFilter) (domain.SuitePage, error) {
	f.tenantID, f.filter = tenantID, filter
	return domain.SuitePage{}, nil
}
func (f *fakeEvaluationQueries) ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error) {
	return domain.RunPage{}, nil
}
func (f *fakeEvaluationQueries) ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error) {
	return domain.CandidatePage{Items: []domain.CandidateSummary{{ID: "candidate-1", SafeDiff: domain.CandidateSafeDiff{
		ChangedFields: []string{"label"}, Changes: map[string]domain.SafeFieldChange{
			"label": {Before: "old", After: "new"},
		},
	}}}}, nil
}
func (f *fakeEvaluationQueries) ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error) {
	return domain.ExperimentPage{Items: []domain.ExperimentSummary{{ID: "experiment-1", PromotionEvidence: domain.PromotionEvidence{
		Eligible: true, Gates: domain.PromotionGates{Quality: domain.GatePassed, Cost: domain.GatePassed,
			Latency: domain.GatePassed, ErrorRate: domain.GatePassed, Security: domain.GatePassed},
		Blockers: []domain.PromotionBlocker{},
	}}}}, nil
}
func (f *fakeEvaluationQueries) Timeline(context.Context, string, port.CenterFilter) (domain.TimelinePage, error) {
	return domain.TimelinePage{}, nil
}

type fakeCandidateCommands struct {
	tenantID, candidateID string
	input                 application.CandidateCommandInput
}

func (f *fakeCandidateCommands) Reject(_ context.Context, tenantID, candidateID string, input application.CandidateCommandInput) (domain.CandidateSummary, error) {
	f.tenantID, f.candidateID, f.input = tenantID, candidateID, input
	return domain.CandidateSummary{ID: candidateID, Status: "rejected"}, nil
}

type fakeExperimentCommands struct{ evaluateKeys []string }

func (*fakeExperimentCommands) Create(context.Context, string, application.CreateExperimentInput) (domain.Experiment, domain.Deployment, error) {
	return domain.Experiment{}, domain.Deployment{}, nil
}
func (f *fakeExperimentCommands) EvaluateStageIdempotent(_ context.Context, _, _ string, input application.EvaluateStageInput) (domain.Experiment, domain.Decision, error) {
	f.evaluateKeys = append(f.evaluateKeys, input.IdempotencyKey)
	return domain.Experiment{}, domain.DecisionHold, nil
}
func (*fakeExperimentCommands) Pause(context.Context, string, string, application.ExperimentCommandInput) (domain.Experiment, error) {
	return domain.Experiment{}, nil
}
func (*fakeExperimentCommands) Promote(context.Context, string, string, application.ExperimentCommandInput) (domain.Experiment, error) {
	return domain.Experiment{}, nil
}
func (*fakeExperimentCommands) Rollback(context.Context, string, string, application.ExperimentCommandInput) (domain.Experiment, error) {
	return domain.Experiment{}, nil
}

func withTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), tenantID))
		c.Next()
	}
}

type fakeEvaluationJobs struct{ tenantID string }

type fakeEvaluationBaselines struct {
	tenantID, resourceID string
	kind                 domain.ResourceKind
}

func (f *fakeEvaluationBaselines) CreatePublishedBaseline(
	_ context.Context, tenantID string, kind domain.ResourceKind, resourceID string,
) (domain.ResourceRef, error) {
	f.tenantID, f.kind, f.resourceID = tenantID, kind, resourceID
	return domain.ResourceRef{Kind: kind, ResourceID: resourceID, RevisionID: "revision-1"}, nil
}

func (f *fakeEvaluationJobs) EnqueueRun(_ context.Context, tenantID string, _ application.EnqueueRunInput) (domain.EvaluationJob, error) {
	f.tenantID = tenantID
	return domain.EvaluationJob{ID: "job-1", Status: domain.JobQueued}, nil
}

func (f *fakeEvaluationJobs) Get(_ context.Context, _ string, _ string) (domain.EvaluationJob, error) {
	return domain.EvaluationJob{ID: "job-1", Status: domain.JobQueued}, nil
}

type fakeOptimizationService struct {
	input application.GenerateCandidatesInput
	err   error
}

func (f *fakeOptimizationService) Generate(
	_ context.Context, _ string, input application.GenerateCandidatesInput,
) (domain.OptimizationJob, []domain.OptimizationCandidate, error) {
	f.input = input
	if f.err != nil {
		return domain.OptimizationJob{}, nil, f.err
	}
	job := domain.OptimizationJob{ID: "optimization-1", Baseline: input.Baseline, Status: domain.JobSucceeded}
	return job, []domain.OptimizationCandidate{{
		ID: "candidate-record-1", OptimizationJobID: job.ID,
		Revision: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "candidate-1"},
	}}, nil
}
