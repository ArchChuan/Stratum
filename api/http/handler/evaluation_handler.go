package handler

import (
	"context"
	"net/http"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/byteBuilderX/stratum/api/middleware"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
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
	EvaluateStage(
		ctx context.Context,
		tenantID, experimentID string,
		metrics domain.StageMetrics,
	) (domain.Experiment, domain.Decision, error)
}

type EvaluationHandler struct {
	suites       evaluationSuiteService
	jobs         evaluationJobService
	runs         evaluationRunService
	optimization evaluationOptimizationService
	experiments  evaluationExperimentService
	logger       *zap.Logger
}

func NewEvaluationHandler(
	suites evaluationSuiteService,
	jobs evaluationJobService,
	runs evaluationRunService,
	optimization evaluationOptimizationService,
	experiments evaluationExperimentService,
	logger *zap.Logger,
) *EvaluationHandler {
	return &EvaluationHandler{
		suites: suites, jobs: jobs, runs: runs, optimization: optimization, experiments: experiments, logger: logger,
	}
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
	experiment, decision, err := h.experiments.EvaluateStage(c.Request.Context(), tenantID, c.Param("id"), domain.StageMetrics{
		Samples: req.Samples, ObservedMinutes: req.ObservedMinutes,
		QualityImprovement: req.QualityImprovement, QualitySignificant: req.QualitySignificant,
		CostRegression: req.CostRegression, P95LatencyRegression: req.P95LatencyRegression,
		ErrorRateIncrease: req.ErrorRateIncrease, SecurityViolation: req.SecurityViolation,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"experiment": experiment, "decision": decision})
}
