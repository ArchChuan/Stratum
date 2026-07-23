package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type evaluationSuiteService interface {
	Create(ctx context.Context, tenantID string, input evalapp.CreateSuiteInput) (domain.EvalSuite, domain.EvalSuiteRevision, error)
	Publish(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error)
}

type evaluationJobService interface {
	EnqueueRun(ctx context.Context, tenantID string, input evalapp.EnqueueRunInput) (domain.EvaluationJob, error)
	Get(ctx context.Context, tenantID, jobID string) (domain.EvaluationJob, error)
}

type evaluationRunService interface {
	GetRun(ctx context.Context, tenantID, runID string) (domain.EvalRun, error)
}

type evaluationOptimizationService interface {
	Generate(
		ctx context.Context,
		tenantID string,
		input evalapp.GenerateCandidatesInput,
	) (domain.OptimizationJob, []domain.OptimizationCandidate, error)
}

type evaluationExperimentService interface {
	Create(
		ctx context.Context,
		tenantID string,
		input evalapp.CreateExperimentInput,
	) (domain.Experiment, domain.Deployment, error)
	EvaluateStageIdempotent(context.Context, string, string, evalapp.EvaluateStageInput) (domain.Experiment, domain.Decision, error)
	Pause(context.Context, string, string, evalapp.ExperimentCommandInput) (domain.Experiment, error)
	Promote(context.Context, string, string, evalapp.ExperimentCommandInput) (domain.Experiment, error)
	Rollback(context.Context, string, string, evalapp.ExperimentCommandInput) (domain.Experiment, error)
}

type evaluationQueryService interface {
	Overview(context.Context, string) (domain.CenterOverview, error)
	ListResources(context.Context, string, port.CenterFilter) (domain.ResourcePage, error)
	ListSuites(context.Context, string, port.CenterFilter) (domain.SuitePage, error)
	ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error)
	ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error)
	ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error)
	Timeline(context.Context, string, port.CenterFilter) (domain.TimelinePage, error)
}

type evaluationCandidateCommandService interface {
	Reject(context.Context, string, string, evalapp.CandidateCommandInput) (domain.CandidateSummary, error)
}

type evaluationFeedbackService interface {
	Record(
		ctx context.Context,
		tenantID string,
		input evalapp.RecordFeedbackInput,
	) (evalapp.FeedbackResult, error)
}

type evaluationBaselineService interface {
	CreatePublishedBaseline(
		ctx context.Context, tenantID string, kind domain.ResourceKind, resourceID string,
	) (domain.ResourceRef, error)
}

type EvaluationHandler struct {
	suites       evaluationSuiteService
	jobs         evaluationJobService
	runs         evaluationRunService
	optimization evaluationOptimizationService
	experiments  evaluationExperimentService
	feedback     evaluationFeedbackService
	queries      evaluationQueryService
	candidates   evaluationCandidateCommandService
	baselines    evaluationBaselineService
	logger       *zap.Logger
}

func NewEvaluationHandler(
	suites evaluationSuiteService,
	jobs evaluationJobService,
	runs evaluationRunService,
	optimization evaluationOptimizationService,
	experiments evaluationExperimentService,
	feedback evaluationFeedbackService,
	queries evaluationQueryService,
	candidates evaluationCandidateCommandService,
	logger *zap.Logger,
) *EvaluationHandler {
	return &EvaluationHandler{
		suites: suites, jobs: jobs, runs: runs, optimization: optimization,
		experiments: experiments, feedback: feedback, queries: queries, candidates: candidates, logger: logger,
	}
}

func (h *EvaluationHandler) WithBaselineService(service evaluationBaselineService) *EvaluationHandler {
	h.baselines = service
	return h
}

func (h *EvaluationHandler) CreateBaseline(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.baselines == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation baseline unavailable")))
		return
	}
	kind := domain.ResourceKind(c.Param("kind"))
	if err := kind.Validate(); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	ref, err := h.baselines.CreatePublishedBaseline(c.Request.Context(), tenantID, kind, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, ref)
}

func (h *EvaluationHandler) CreateSuite(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.CreateEvaluationSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	cases := make([]domain.EvalCase, 0, len(req.Cases))
	for _, item := range req.Cases {
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		cases = append(cases, domain.EvalCase{
			Name: item.Name, Input: item.Input, ExpectedOutput: item.ExpectedOutput,
			AssertionMode: domain.AssertionMode(item.AssertionMode), Enabled: enabled,
		})
	}
	suite, revision, err := h.suites.Create(c.Request.Context(), tenantID, evalapp.CreateSuiteInput{
		Name: req.Name, Description: req.Description, ResourceKind: domain.ResourceKind(req.ResourceKind), Cases: cases,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"suite": suite, "revision": revision})
}

func (h *EvaluationHandler) PublishSuite(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	revision, err := h.suites.Publish(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revision)
}

func (h *EvaluationHandler) EnqueueRun(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	requestedBy, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	var req dto.EnqueueEvaluationRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	job, err := h.jobs.EnqueueRun(c.Request.Context(), tenantID, evalapp.EnqueueRunInput{
		Resource: domain.ResourceRef{
			Kind: domain.ResourceKind(req.Resource.Kind), ResourceID: req.Resource.ResourceID, RevisionID: req.Resource.RevisionID,
		},
		SuiteRevisionID: req.SuiteRevisionID,
		IdempotencyKey:  req.IdempotencyKey,
		RequestedBy:     requestedBy,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, dto.EvaluationJobResponse{JobID: job.ID, Status: string(job.Status)})
}

func (h *EvaluationHandler) GetJob(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	job, err := h.jobs.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, dto.EvaluationJobResponse{
		JobID: job.ID, Status: string(job.Status), ErrorMessage: job.ErrorMessage, ResultID: job.ResultID,
	})
}

func (h *EvaluationHandler) GetRun(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	run, err := h.runs.GetRun(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, run)
}

func (h *EvaluationHandler) GenerateOptimization(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.GenerateOptimizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	job, candidates, err := h.optimization.Generate(c.Request.Context(), tenantID, evalapp.GenerateCandidatesInput{
		IdempotencyKey: firstNonEmpty(req.IdempotencyKey, c.GetHeader("Idempotency-Key")),
		Baseline: domain.ResourceRef{
			Kind: domain.ResourceKind(req.Baseline.Kind), ResourceID: req.Baseline.ResourceID,
			RevisionID: req.Baseline.RevisionID,
		},
		SuiteRevisionID: req.SuiteRevisionID, SearchSpace: req.SearchSpace,
		FailureSummaries: req.FailureSummaries,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"job": job, "candidates": candidates})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (h *EvaluationHandler) CreateExperiment(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.CreateEvaluationExperimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	toRef := func(ref dto.EvaluationResourceRef) domain.ResourceRef {
		return domain.ResourceRef{Kind: domain.ResourceKind(ref.Kind), ResourceID: ref.ResourceID, RevisionID: ref.RevisionID}
	}
	experiment, deployment, err := h.experiments.Create(c.Request.Context(), tenantID, evalapp.CreateExperimentInput{
		Stable: toRef(req.Stable), Canary: toRef(req.Canary), SuiteRevisionID: req.SuiteRevisionID,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"experiment": experiment, "deployment": deployment})
}

func (h *EvaluationHandler) EvaluateExperiment(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.EvaluateExperimentStageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = c.GetHeader("Idempotency-Key")
	}
	if idempotencyKey == "" {
		metrics := domain.StageMetrics{
			Samples: req.Samples, ObservedMinutes: req.ObservedMinutes,
			QualityImprovement: req.QualityImprovement, QualitySignificant: req.QualitySignificant,
			CostRegression: req.CostRegression, P95LatencyRegression: req.P95LatencyRegression,
			ErrorRateIncrease: req.ErrorRateIncrease, SecurityViolation: req.SecurityViolation,
		}
		idempotencyKey = "legacy-evaluate-" + domain.MetricsFingerprint(metrics)
	}
	experiment, decision, err := h.experiments.EvaluateStageIdempotent(c.Request.Context(), tenantID, c.Param("id"), evalapp.EvaluateStageInput{Metrics: domain.StageMetrics{
		Samples: req.Samples, ObservedMinutes: req.ObservedMinutes,
		QualityImprovement: req.QualityImprovement, QualitySignificant: req.QualitySignificant,
		CostRegression: req.CostRegression, P95LatencyRegression: req.P95LatencyRegression,
		ErrorRateIncrease: req.ErrorRateIncrease, SecurityViolation: req.SecurityViolation,
	}, IdempotencyKey: idempotencyKey})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"experiment": experiment, "decision": decision})
}

func (h *EvaluationHandler) Overview(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	result, err := h.queries.Overview(c.Request.Context(), tenantID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func centerFilter(c *gin.Context, kind, id string) (port.CenterFilter, error) {
	var req dto.EvaluationCenterQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		return port.CenterFilter{}, err
	}
	if kind != "" {
		req.ResourceKind = kind
	}
	if id != "" {
		req.ResourceID = id
	}
	return port.CenterFilter{ResourceKind: req.ResourceKind, ResourceID: req.ResourceID, Status: req.Status,
		Cursor: req.Cursor, Limit: req.Limit}, nil
}

func queryPage[T any](c *gin.Context, call func(string, port.CenterFilter) (T, error), kind, id string) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	filter, err := centerFilter(c, kind, id)
	if err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	page, err := call(tenantID, filter)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *EvaluationHandler) ListResources(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.ResourcePage, error) {
		return h.queries.ListResources(c.Request.Context(), t, f)
	}, "", "")
}
func (h *EvaluationHandler) ListSuites(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.SuitePage, error) {
		return h.queries.ListSuites(c.Request.Context(), t, f)
	}, "", "")
}
func (h *EvaluationHandler) ListRuns(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.RunPage, error) {
		return h.queries.ListRuns(c.Request.Context(), t, f)
	}, "", "")
}
func (h *EvaluationHandler) ListCandidates(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.CandidatePage, error) {
		return h.queries.ListCandidates(c.Request.Context(), t, f)
	}, "", "")
}
func (h *EvaluationHandler) ListExperiments(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.ExperimentPage, error) {
		return h.queries.ListExperiments(c.Request.Context(), t, f)
	}, "", "")
}
func (h *EvaluationHandler) Timeline(c *gin.Context) {
	queryPage(c, func(t string, f port.CenterFilter) (domain.TimelinePage, error) {
		return h.queries.Timeline(c.Request.Context(), t, f)
	}, c.Param("kind"), c.Param("id"))
}

func commandInput(c *gin.Context) (evalapp.ExperimentCommandInput, bool) {
	var req dto.EvaluationCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return evalapp.ExperimentCommandInput{}, false
	}
	actorID, ok := userIDFromCtx(c)
	if !ok || actorID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("authenticated actor required")))
		return evalapp.ExperimentCommandInput{}, false
	}
	return evalapp.ExperimentCommandInput{ActorID: actorID, Reason: req.Reason, IdempotencyKey: req.IdempotencyKey,
		ExpectedStateVersion: req.ExpectedStateVersion}, true
}

func (h *EvaluationHandler) experimentCommand(c *gin.Context, call func(context.Context, string, string, evalapp.ExperimentCommandInput) (domain.Experiment, error)) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	input, ok := commandInput(c)
	if !ok {
		return
	}
	result, err := call(c.Request.Context(), tenantID, c.Param("id"), input)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (h *EvaluationHandler) PauseExperiment(c *gin.Context) {
	h.experimentCommand(c, h.experiments.Pause)
}
func (h *EvaluationHandler) PromoteExperiment(c *gin.Context) {
	h.experimentCommand(c, h.experiments.Promote)
}
func (h *EvaluationHandler) RollbackExperiment(c *gin.Context) {
	h.experimentCommand(c, h.experiments.Rollback)
}

func (h *EvaluationHandler) RejectCandidate(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	input, ok := commandInput(c)
	if !ok {
		return
	}
	result, err := h.candidates.Reject(c.Request.Context(), tenantID, c.Param("id"), evalapp.CandidateCommandInput(input))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *EvaluationHandler) RecordFeedback(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req dto.RecordEvaluationFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	result, err := h.feedback.Record(c.Request.Context(), tenantID, evalapp.RecordFeedbackInput{
		TraceID: req.TraceID, ResourceKind: domain.ResourceKind(req.ResourceKind), ResourceID: req.ResourceID,
		Score: req.Score, Outcome: req.Outcome, IdempotencyKey: req.IdempotencyKey,
		SecurityViolation: req.SecurityViolation,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, result)
}
