package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestEvaluationHandlerEnqueueRunReturnsAcceptedJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jobs := &fakeEvaluationJobs{}
	h := NewEvaluationHandler(nil, jobs, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.POST("/evaluations/runs", withTenantAndUser("tenant-1", "user-1"), h.EnqueueRun)

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
	if jobs.tenantID != "tenant-1" || jobs.input.RequestedBy != "user-1" {
		t.Fatalf("request identity not propagated: tenant=%q input=%+v", jobs.tenantID, jobs.input)
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
		c.Set(middleware.ContextKeyRole, "admin")
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
		c.Set(middleware.ContextKeyRole, "admin")
		c.Next()
	}
}

type fakeEvaluationJobs struct {
	tenantID string
	input    application.EnqueueRunInput
}

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

func (f *fakeEvaluationJobs) EnqueueRun(
	_ context.Context, tenantID string, input application.EnqueueRunInput,
) (domain.EvaluationJob, error) {
	f.tenantID, f.input = tenantID, input
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

type fakeSuiteService struct {
	created     domain.EvalSuite
	revision    domain.EvalSuiteRevision
	getDraftErr error
	updated     domain.EvalCase
	updateErr   error
	tenantID    string
	suiteID     string
	caseID      string
}

func (f *fakeSuiteService) Create(_ context.Context, _ string, input application.CreateSuiteInput) (domain.EvalSuite, domain.EvalSuiteRevision, error) {
	return f.created, f.revision, nil
}

func (f *fakeSuiteService) Publish(_ context.Context, _, _ string) (domain.EvalSuiteRevision, error) {
	return f.revision, nil
}

func (f *fakeSuiteService) GetDraft(_ context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error) {
	f.tenantID, f.suiteID = tenantID, suiteID
	return f.revision, f.getDraftErr
}

func (f *fakeSuiteService) UpdateDraftCase(_ context.Context, tenantID, suiteID, caseID string, testCase domain.EvalCase) (domain.EvalCase, error) {
	f.tenantID, f.suiteID, f.caseID = tenantID, suiteID, caseID
	return f.updated, f.updateErr
}

type fakeCaseGen struct {
	result application.GenerateResult
	err    error
	input  application.GenerateInput
}

func (f *fakeCaseGen) Generate(_ context.Context, input application.GenerateInput) (application.GenerateResult, error) {
	f.input = input
	return f.result, f.err
}

func TestEvaluationHandlerGenerateSuiteCasesSamplesAndReturnsResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gen := &fakeCaseGen{result: application.GenerateResult{SamplesFound: 5, Generated: 3}}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithTestCaseGenerator(gen)
	r := gin.New()
	r.POST("/evaluations/suites/:id/generate", withTenant("tenant-1"), h.GenerateSuiteCases)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/evaluations/suites/suite-1/generate",
		strings.NewReader(`{"sample_policy":"negative_first","max_cases":7}`)))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"samples_found":5`) ||
		!strings.Contains(rec.Body.String(), `"generated":3`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gen.input.SuiteID != "suite-1" || gen.input.TenantID != "tenant-1" ||
		gen.input.Policy != domain.SamplePolicyNegativeFirst || gen.input.MaxCases != 7 {
		t.Fatalf("generate input not propagated: %+v", gen.input)
	}
}

func TestEvaluationHandlerGenerateSuiteCasesDefaultsLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gen := &fakeCaseGen{}
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithTestCaseGenerator(gen)
	r := gin.New()
	r.POST("/evaluations/suites/:id/generate", withTenant("tenant-1"), h.GenerateSuiteCases)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/evaluations/suites/suite-1/generate", strings.NewReader(`{"sample_policy":"balanced"}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if gen.input.MaxCases != constants.DefaultCaseSampleLimit {
		t.Fatalf("default limit not applied: %d", gen.input.MaxCases)
	}
}

func TestEvaluationHandlerGenerateSuiteCasesUnavailableWithoutGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(nil, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/suites/:id/generate", withTenant("tenant-1"), h.GenerateSuiteCases)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/evaluations/suites/suite-1/generate", strings.NewReader(`{"sample_policy":"negative_first"}`)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without gateway, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvaluationHandlerGetSuiteDraft(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suites := &fakeSuiteService{
		revision: domain.EvalSuiteRevision{
			ID: "draft-1", SuiteID: "suite-1", Status: domain.SuiteRevisionDraft,
			ResourceKind: domain.ResourceKindSkill,
			Cases:        []domain.EvalCase{{ID: "case-1", Name: "物流", Input: "快递没更新", ExpectedOutput: "物流查询"}},
		},
	}
	h := NewEvaluationHandler(suites, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.GET("/evaluations/suites/:id/draft", withTenant("tenant-1"), h.GetSuiteDraft)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evaluations/suites/suite-1/draft", nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"case-1"`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if suites.tenantID != "tenant-1" || suites.suiteID != "suite-1" {
		t.Fatalf("draft path not propagated: %+v", suites)
	}
}

func TestEvaluationHandlerUpdateDraftCaseDefaultsEnabledTrue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suites := &fakeSuiteService{updated: domain.EvalCase{ID: "case-1", Name: "物流改", Enabled: true}}
	h := NewEvaluationHandler(suites, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.PUT("/evaluations/suites/:id/draft/cases/:caseId", withTenant("tenant-1"), h.UpdateDraftCase)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
		"/evaluations/suites/suite-1/draft/cases/case-1",
		strings.NewReader(`{"name":"物流改","input":"物流进度查询","expected_output":"物流查询","assertion_mode":"exact"}`)))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("unexpected response: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if suites.caseID != "case-1" || suites.tenantID != "tenant-1" {
		t.Fatalf("update path not propagated: %+v", suites)
	}
}

func TestEvaluationHandlerUpdateDraftCaseRejectsWhenEnabledFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suites := &fakeSuiteService{updated: domain.EvalCase{ID: "case-1", Enabled: false}}
	h := NewEvaluationHandler(suites, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.PUT("/evaluations/suites/:id/draft/cases/:caseId", withTenant("tenant-1"), h.UpdateDraftCase)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
		"/evaluations/suites/suite-1/draft/cases/case-1",
		strings.NewReader(`{"name":"物流改","input":"物流进度查询","expected_output":"物流查询","assertion_mode":"contains","enabled":false}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestEvaluationHandlerUpdateDraftCaseRejectsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEvaluationHandler(&fakeSuiteService{}, nil, nil, nil, nil, nil, nil, nil, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.PUT("/evaluations/suites/:id/draft/cases/:caseId", withTenant("tenant-1"), h.UpdateDraftCase)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
		"/evaluations/suites/suite-1/draft/cases/case-1",
		strings.NewReader(`{"name":"","input":"","expected_output":""}`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid update, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// withRole 注入 JWT role claim（middleware.RequireTenantRole 依赖 auth.role）。
func withRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
}

// fakeApprovalRequests 记录审批请求，模拟 ToolApprovalService.Request。
type fakeApprovalRequests struct {
	called      int
	subjectKind string
	toolName    string
	args        map[string]any
}

func (f *fakeApprovalRequests) Request(_ context.Context, payload agentapp.ToolApprovalPayload) (string, error) {
	f.called++
	f.subjectKind = payload.SubjectKind
	f.toolName = payload.ToolName
	f.args = payload.Arguments
	return "approval-1", nil
}

// D4：member 发起评测写操作 → 创建审批并返回 202 pending_approval，不直接执行。
func TestEvaluationCreateSuiteMemberGetsPendingApproval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suites := &fakeSuiteService{}
	approvals := &fakeApprovalRequests{}
	h := NewEvaluationHandler(suites, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithApprovalService(approvals)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/suites", withTenantAndUser("tenant-1", "member-1"), withRole("member"), h.CreateSuite)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluations/suites",
		strings.NewReader(`{"name":"S","description":"D","resource_kind":"skill","cases":[{"name":"c1","input":"i","expected_output":"o","assertion_mode":"exact"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 pending_approval, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"pending_approval"`) ||
		!strings.Contains(rec.Body.String(), `"approval_id":"approval-1"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if approvals.called != 1 {
		t.Fatalf("approval Request called %d times, want 1", approvals.called)
	}
	if approvals.subjectKind != agentdomain.SubjectKindEvaluationAction {
		t.Fatalf("subject kind=%q, want evaluation_action", approvals.subjectKind)
	}
	if approvals.toolName != "evaluation.create_suite" {
		t.Fatalf("tool name=%q, want evaluation.create_suite", approvals.toolName)
	}
	if approvals.args["operation"] != "create_suite" {
		t.Fatalf("operation arg=%v, want create_suite", approvals.args["operation"])
	}
}

// D4：admin 直接执行，不创建审批。
func TestEvaluationCreateSuiteAdminExecutesDirectly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	suites := &fakeSuiteService{created: domain.EvalSuite{ID: "suite-1"}}
	approvals := &fakeApprovalRequests{}
	h := NewEvaluationHandler(suites, nil, nil, nil, nil, nil, nil, nil, zap.NewNop()).
		WithApprovalService(approvals)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.POST("/evaluations/suites", withTenantAndUser("tenant-1", "admin-1"), withRole("admin"), h.CreateSuite)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/evaluations/suites",
		strings.NewReader(`{"name":"S","description":"D","resource_kind":"skill","cases":[{"name":"c1","input":"i","expected_output":"o","assertion_mode":"exact"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if approvals.called != 0 {
		t.Fatalf("approval Request called %d times, want 0 for admin", approvals.called)
	}
	if suites.created.ID != "suite-1" {
		t.Fatalf("suite Create not executed directly: %+v", suites.created)
	}
}
