package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type evaluationSuiteService interface {
	Create(ctx context.Context, tenantID string, input evalapp.CreateSuiteInput) (domain.EvalSuite, domain.EvalSuiteRevision, error)
	Publish(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error)
	GetDraft(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, error)
	UpdateDraftCase(ctx context.Context, tenantID, suiteID, caseID string, testCase domain.EvalCase) (domain.EvalCase, error)
}

type evaluationCaseGenerator interface {
	Generate(ctx context.Context, input evalapp.GenerateInput) (evalapp.GenerateResult, error)
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

type evaluationAgentRevisionApplier interface {
	ApplyPublishedRevision(ctx context.Context, tenantID, agentID, revisionID string) error
}

// evaluationApprovalService 创建审批请求（D4 member 写操作分流）。
// *agentapp.ToolApprovalService 满足该接口。
type evaluationApprovalService interface {
	Request(ctx context.Context, payload agentapp.ToolApprovalPayload) (string, error)
}

// evaluationRoleResolver 现查 actor 的租户角色（单事实源）。wiring 注入
// tenantRoleAdapter；resolver 缺失（测试/降级）时 currentRole 回退 JWT role claim。
type evaluationRoleResolver interface {
	ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error)
}

// evaluationObservationQueryService 运行态观测查询服务（P1a 查询 API）。
type evaluationObservationQueryService interface {
	ListObservations(ctx context.Context, tenantID, resourceKind, resourceID string,
		from, to *time.Time, limit, offset int) ([]domain.EvalObservation, error)
	GetObservation(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error)
}

// evaluationReviewService 评审池查询与人工决策服务（P1c）。*evalapp.ReviewService 满足该接口。
type evaluationReviewService interface {
	List(ctx context.Context, tenantID string, f port.ReviewFilter) ([]domain.ReviewItem, int64, error)
	Get(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error)
	Decide(ctx context.Context, tenantID, id, actor string, verdict domain.HumanVerdict,
		reason string) (*domain.ReviewItem, error)
}

// policyVersionEvaluationAction 标记评测动作审批的策略版本（审批协议演进时的
// 兼容性锚点；Digest 校验使其与创建时版本强绑定）。
const policyVersionEvaluationAction = "action-v1"

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
	agentApplier evaluationAgentRevisionApplier
	casegen      evaluationCaseGenerator
	approvals    evaluationApprovalService
	roles        evaluationRoleResolver
	observations evaluationObservationQueryService
	review       evaluationReviewService
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

func (h *EvaluationHandler) WithAgentRevisionApplier(applier evaluationAgentRevisionApplier) *EvaluationHandler {
	h.agentApplier = applier
	return h
}

func (h *EvaluationHandler) WithTestCaseGenerator(generator evaluationCaseGenerator) *EvaluationHandler {
	h.casegen = generator
	return h
}

func (h *EvaluationHandler) WithApprovalService(service evaluationApprovalService) *EvaluationHandler {
	h.approvals = service
	return h
}

// WithRoleResolver 注入租户角色现查 resolver（wiring 提供 DB-backed adapter）。
// resolver 未注入时 currentRole 回退 JWT role claim（仅测试路径）。
func (h *EvaluationHandler) WithRoleResolver(roles evaluationRoleResolver) *EvaluationHandler {
	h.roles = roles
	return h
}

// WithObservationService 注入运行态观测查询服务（P1a 查询 API）。
func (h *EvaluationHandler) WithObservationService(service evaluationObservationQueryService) *EvaluationHandler {
	h.observations = service
	return h
}

// WithReviewService 注入评审池查询与决策服务（P1c）。
func (h *EvaluationHandler) WithReviewService(service evaluationReviewService) *EvaluationHandler {
	h.review = service
	return h
}

// currentRole 优先现查（单事实源，fail closed）；identity 缺失或解析失败返回 ""。
// 仅当 resolver 未装配（测试/降级）时回退 JWT role claim。
func (h *EvaluationHandler) currentRole(c *gin.Context) string {
	if h.roles != nil {
		tenantID, ok := tenantIDFromCtx(c)
		if !ok {
			return ""
		}
		actor, ok := userIDFromCtx(c)
		if !ok {
			return ""
		}
		role, err := h.roles.ResolveTenantRole(c.Request.Context(), tenantID, actor)
		if err != nil {
			return ""
		}
		return role
	}
	return c.GetString(middleware.ContextKeyRole)
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
	args := map[string]any{"operation": "create_baseline", "resourceKind": string(kind), "resourceID": c.Param("id")}
	h.requireApprovalOrExecute(c, "evaluation.create_baseline", args, http.StatusCreated, func() (any, error) {
		ref, err := h.baselines.CreatePublishedBaseline(c.Request.Context(), tenantID, kind, c.Param("id"))
		if err != nil {
			return nil, err
		}
		return ref, nil
	})
}

func (h *EvaluationHandler) CreateSuite(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req gen.CreateEvaluationSuiteRequest
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
	caseArgs := make([]any, 0, len(cases))
	for _, cs := range cases {
		caseArgs = append(caseArgs, map[string]any{
			"name": cs.Name, "input": cs.Input, "expected_output": cs.ExpectedOutput,
			"assertion_mode": string(cs.AssertionMode), "enabled": cs.Enabled,
		})
	}
	args := map[string]any{"operation": "create_suite", "name": req.Name, "description": req.Description,
		"resource_kind": string(req.ResourceKind), "cases": caseArgs}
	h.requireApprovalOrExecute(c, "evaluation.create_suite", args, http.StatusCreated, func() (any, error) {
		suite, revision, err := h.suites.Create(c.Request.Context(), tenantID, evalapp.CreateSuiteInput{
			Name: req.Name, Description: req.Description, ResourceKind: domain.ResourceKind(req.ResourceKind), Cases: cases,
		})
		if err != nil {
			return nil, err
		}
		return gin.H{"suite": suite, "revision": revision}, nil
	})
}

func (h *EvaluationHandler) PublishSuite(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	args := map[string]any{"operation": "publish_suite", "suiteID": c.Param("id")}
	h.requireApprovalOrExecute(c, "evaluation.publish_suite", args, http.StatusOK, func() (any, error) {
		revision, err := h.suites.Publish(c.Request.Context(), tenantID, c.Param("id"))
		if err != nil {
			return nil, err
		}
		return revision, nil
	})
}

// GenerateSuiteCases samples production interactions for the suite's
// resource kind and writes generated cases into its draft revision for
// human review. The generator never publishes automatically.
func (h *EvaluationHandler) GenerateSuiteCases(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.casegen == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("case generator unavailable")))
		return
	}
	var req gen.GenerateSuiteCasesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	limit := constants.DefaultCaseSampleLimit
	if req.MaxCases > 0 {
		limit = int(req.MaxCases)
	}
	args := map[string]any{"operation": "generate_suite_cases", "suiteID": c.Param("id"),
		"samplePolicy": string(req.SamplePolicy), "maxCases": limit}
	h.requireApprovalOrExecute(c, "evaluation.generate_suite_cases", args, http.StatusOK, func() (any, error) {
		result, err := h.casegen.Generate(c.Request.Context(), evalapp.GenerateInput{
			TenantID: tenantID,
			SuiteID:  c.Param("id"),
			Policy:   domain.SamplePolicy(req.SamplePolicy),
			MaxCases: limit,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func (h *EvaluationHandler) GetSuiteDraft(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	revision, err := h.suites.GetDraft(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, revision)
}

// UpdateDraftCase approves (enabled=true), edits (full replacement) or
// rejects (enabled=false) one case in the suite's draft revision.
func (h *EvaluationHandler) UpdateDraftCase(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	var req gen.UpdateDraftCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	updated, err := h.suites.UpdateDraftCase(c.Request.Context(), tenantID, c.Param("id"), c.Param("caseId"), domain.EvalCase{
		ID:             c.Param("caseId"),
		Name:           req.Name,
		Input:          req.Input,
		ExpectedOutput: req.ExpectedOutput,
		AssertionMode:  domain.AssertionMode(req.AssertionMode),
		Enabled:        enabled,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, updated)
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
	var req gen.EnqueueEvaluationRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	args := map[string]any{"operation": "enqueue_run",
		"resource":        map[string]any{"kind": string(req.Resource.Kind), "id": req.Resource.ResourceID, "revision_id": req.Resource.RevisionID},
		"suiteRevisionID": req.SuiteRevisionID, "idempotencyKey": req.IdempotencyKey}
	h.requireApprovalOrExecute(c, "evaluation.enqueue_run", args, http.StatusAccepted, func() (any, error) {
		job, err := h.jobs.EnqueueRun(c.Request.Context(), tenantID, evalapp.EnqueueRunInput{
			Resource: domain.ResourceRef{
				Kind: domain.ResourceKind(req.Resource.Kind), ResourceID: req.Resource.ResourceID, RevisionID: req.Resource.RevisionID,
			},
			SuiteRevisionID: req.SuiteRevisionID,
			IdempotencyKey:  req.IdempotencyKey,
			RequestedBy:     requestedBy,
		})
		if err != nil {
			return nil, err
		}
		return gen.EvaluationJobResponse{JobID: job.ID, Status: string(job.Status)}, nil
	})
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
	c.JSON(http.StatusOK, gen.EvaluationJobResponse{
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
	var req gen.GenerateOptimizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	args := map[string]any{"operation": "generate_optimization",
		"idempotencyKey":  firstNonEmpty(req.IdempotencyKey, c.GetHeader("Idempotency-Key")),
		"baseline":        map[string]any{"kind": string(req.Baseline.Kind), "id": req.Baseline.ResourceID, "revision_id": req.Baseline.RevisionID},
		"suiteRevisionID": req.SuiteRevisionID, "searchSpace": req.SearchSpace, "failureSummaries": req.FailureSummaries}
	h.requireApprovalOrExecute(c, "evaluation.generate_optimization", args, http.StatusCreated, func() (any, error) {
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
			return nil, err
		}
		return gin.H{"job": job, "candidates": candidates}, nil
	})
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
	var req gen.CreateEvaluationExperimentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	toRef := func(ref gen.EvaluationResourceRef) domain.ResourceRef {
		return domain.ResourceRef{Kind: domain.ResourceKind(ref.Kind), ResourceID: ref.ResourceID, RevisionID: ref.RevisionID}
	}
	stable, canary := toRef(req.Stable), toRef(req.Canary)
	args := map[string]any{"operation": "create_experiment",
		"stable":          map[string]any{"kind": string(stable.Kind), "id": stable.ResourceID, "revision_id": stable.RevisionID},
		"canary":          map[string]any{"kind": string(canary.Kind), "id": canary.ResourceID, "revision_id": canary.RevisionID},
		"suiteRevisionID": req.SuiteRevisionID}
	h.requireApprovalOrExecute(c, "evaluation.create_experiment", args, http.StatusCreated, func() (any, error) {
		actorID, _ := userIDFromCtx(c)
		experiment, deployment, err := h.experiments.Create(c.Request.Context(), tenantID, evalapp.CreateExperimentInput{
			Stable: stable, Canary: canary, SuiteRevisionID: req.SuiteRevisionID, ActorID: actorID,
		})
		if err != nil {
			return nil, err
		}
		return gin.H{"experiment": experiment, "deployment": deployment}, nil
	})
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
	var req gen.EvaluationCenterQuery
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
	var req gen.EvaluationCommandRequest
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

// experimentCommand 解析命令入参后按角色分流（member → 审批，admin/owner → 直接执行）。
// commandInput 已消费请求体并校验 actor，因此 args 中的 reason 等字段直接取自解析结果。
func (h *EvaluationHandler) experimentCommand(c *gin.Context, operation string, call func(context.Context, string, string, evalapp.ExperimentCommandInput) (domain.Experiment, error)) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	input, ok := commandInput(c)
	if !ok {
		return
	}
	args := map[string]any{"operation": operation, "experimentID": c.Param("id"),
		"reason": input.Reason, "idempotencyKey": input.IdempotencyKey,
		"expectedStateVersion": input.ExpectedStateVersion}
	h.requireApprovalOrExecute(c, "evaluation."+operation, args, http.StatusOK, func() (any, error) {
		result, err := call(c.Request.Context(), tenantID, c.Param("id"), input)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}
func (h *EvaluationHandler) PauseExperiment(c *gin.Context) {
	h.experimentCommand(c, "pause_experiment", h.experiments.Pause)
}

// PromoteExperiment promotes an experiment's canary to stable.  For Agent
// resources it additionally writes the optimized revision payload back to the
// agents table, closing the evaluation → production loop.
func (h *EvaluationHandler) PromoteExperiment(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	input, ok := commandInput(c)
	if !ok {
		return
	}
	args := map[string]any{"operation": "promote_experiment", "experimentID": c.Param("id"),
		"reason": input.Reason, "idempotencyKey": input.IdempotencyKey,
		"expectedStateVersion": input.ExpectedStateVersion}
	h.requireApprovalOrExecute(c, "evaluation.promote_experiment", args, http.StatusOK, func() (any, error) {
		result, err := h.experiments.Promote(c.Request.Context(), tenantID, c.Param("id"), input)
		if err != nil {
			return nil, err
		}
		// Write optimized Agent revision back to the production agents table.
		if result.ResourceKind == domain.ResourceKindAgent && h.agentApplier != nil {
			if applyErr := h.agentApplier.ApplyPublishedRevision(
				c.Request.Context(), tenantID, result.ResourceID, result.CanaryRevisionID,
			); applyErr != nil {
				h.logger.Warn("promote experiment: agent write-back failed",
					zap.String("agent_id", result.ResourceID),
					zap.String("revision_id", result.CanaryRevisionID),
					zap.Error(applyErr),
				)
			}
		}
		return result, nil
	})
}
func (h *EvaluationHandler) RollbackExperiment(c *gin.Context) {
	h.experimentCommand(c, "rollback_experiment", h.experiments.Rollback)
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
	args := map[string]any{"operation": "reject_candidate", "candidateID": c.Param("id"),
		"reason": input.Reason, "idempotencyKey": input.IdempotencyKey,
		"expectedStateVersion": input.ExpectedStateVersion}
	h.requireApprovalOrExecute(c, "evaluation.reject_candidate", args, http.StatusOK, func() (any, error) {
		result, err := h.candidates.Reject(c.Request.Context(), tenantID, c.Param("id"), evalapp.CandidateCommandInput(input))
		if err != nil {
			return nil, err
		}
		return result, nil
	})
}

func (h *EvaluationHandler) RecordFeedback(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok {
		respondMissingUser(c)
		return
	}
	var req gen.RecordEvaluationFeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	result, err := h.feedback.Record(c.Request.Context(), tenantID, evalapp.RecordFeedbackInput{
		ActorID: actorID, TraceID: req.TraceID, ResourceKind: domain.ResourceKind(req.ResourceKind), ResourceID: req.ResourceID,
		Score: req.Score, Outcome: req.Outcome, IdempotencyKey: req.IdempotencyKey,
		SecurityViolation: req.SecurityViolation,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// evaluationApprovalPayload 构造评测动作审批 payload（D4：member 发起 → 审批）。
// tenant/user 缺失时返回 401（身份上下文必须齐备，否则无法归属审批请求）。
func evaluationApprovalPayload(c *gin.Context, toolName string, args map[string]any) (agentapp.ToolApprovalPayload, error) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		return agentapp.ToolApprovalPayload{}, middleware.NewHTTPError(http.StatusUnauthorized, errMissingTenant)
	}
	userID, ok := userIDFromCtx(c)
	if !ok {
		return agentapp.ToolApprovalPayload{}, middleware.NewHTTPError(http.StatusUnauthorized, errMissingUser)
	}
	return agentapp.ToolApprovalPayload{
		TenantID: tenantID, UserID: userID,
		ExecutionID: uuid.NewString(), ToolCallID: uuid.NewString(),
		ToolName: toolName, SubjectKind: agentdomain.SubjectKindEvaluationAction,
		RiskLevel: agentdomain.ToolRiskUnclassified, Arguments: args, PolicyVersion: policyVersionEvaluationAction,
	}, nil
}

// requestApprovalForMember 创建审批并写 202 响应；返回 true 表示响应已由审批路径消化。
func (h *EvaluationHandler) requestApprovalForMember(c *gin.Context, toolName string, args map[string]any) (bool, error) {
	if h.approvals == nil {
		return false, middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("approval service unavailable"))
	}
	payload, err := evaluationApprovalPayload(c, toolName, args)
	if err != nil {
		return false, err
	}
	id, err := h.approvals.Request(c.Request.Context(), payload)
	if err != nil {
		return false, err
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "pending_approval", "approval_id": id})
	return true, nil
}

// requireApprovalOrExecute 角色分流：admin/owner 直接执行；member 创建审批；
// 角色未知 fail closed（拒绝）。角色由 resolver 现查（单事实源，不信任 JWT claim
// 的陈旧窗口），resolver 未注入时才回退 claim。execute 成功时以 successStatus 写响应。
func (h *EvaluationHandler) requireApprovalOrExecute(c *gin.Context, toolName string, args map[string]any, successStatus int, execute func() (any, error)) {
	roleClass := h.currentRole(c)
	if roleClass != "admin" && roleClass != "owner" {
		if roleClass == "member" {
			handled, err := h.requestApprovalForMember(c, toolName, args)
			if err != nil {
				_ = c.Error(err)
				return
			}
			if handled {
				return
			}
		}
		_ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("insufficient tenant role")))
		return
	}
	result, err := execute()
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(successStatus, result)
}

// ListObservationsQuery 观测分页查询参数（from/to 可选，RFC3339）。
type ListObservationsQuery struct {
	ResourceKind string     `form:"resource_kind"`
	ResourceID   string     `form:"resource_id"`
	From         *time.Time `form:"from" time_format:"2006-01-02T15:04:05Z07:00"`
	To           *time.Time `form:"to" time_format:"2006-01-02T15:04:05Z07:00"`
	Page         int        `form:"page"`
	PageSize     int        `form:"page_size"`
}

// ListObservations 返回运行态观测明细分页（规格 §10.1 数据源）。
func (h *EvaluationHandler) ListObservations(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.observations == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation observation unavailable")))
		return
	}
	var req ListObservationsQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > constants.MaxPageSize {
		req.PageSize = constants.DefaultPageSize
	}
	limit, offset := req.PageSize, (req.Page-1)*req.PageSize
	items, err := h.observations.ListObservations(c.Request.Context(), tenantID,
		req.ResourceKind, req.ResourceID, req.From, req.To, limit, offset)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if items == nil {
		items = []domain.EvalObservation{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

// GetObservation 返回单条运行态观测明细。
func (h *EvaluationHandler) GetObservation(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.observations == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation observation unavailable")))
		return
	}
	obs, err := h.observations.GetObservation(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if obs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "observation not found"})
		return
	}
	c.JSON(http.StatusOK, obs)
}

// ReviewListQuery 评审池分页查询参数（status/trigger_reason 为原始字符串，边界在
// handler 内收敛为领域类型，避免绑定层自定义类型解析依赖）。
type ReviewListQuery struct {
	Status        string `form:"status"`
	TriggerReason string `form:"trigger_reason"`
	ResourceKind  string `form:"resource_kind"`
	ResourceID    string `form:"resource_id"`
	Page          int    `form:"page"`
	PageSize      int    `form:"page_size"`
}

// ListReviewItems 返回评审池分页明细（spec §9 数据源）。
func (h *EvaluationHandler) ListReviewItems(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review unavailable")))
		return
	}
	var req ReviewListQuery
	if err := c.ShouldBindQuery(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > constants.MaxPageSize {
		req.PageSize = constants.DefaultPageSize
	}
	items, total, err := h.review.List(c.Request.Context(), tenantID, port.ReviewFilter{
		Status:        domain.ReviewItemStatus(req.Status),
		TriggerReason: domain.ReviewTriggerReason(req.TriggerReason),
		ResourceKind:  req.ResourceKind,
		ResourceID:    req.ResourceID,
		Limit:         req.PageSize,
		Offset:        (req.Page - 1) * req.PageSize,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	if items == nil {
		items = []domain.ReviewItem{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// GetReviewItem 返回单条评审明细。
func (h *EvaluationHandler) GetReviewItem(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review unavailable")))
		return
	}
	item, err := h.review.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		if errors.Is(err, evalapp.ErrReviewItemNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "review item not found"})
			return
		}
		_ = c.Error(err)
		return
	}
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "review item not found"})
		return
	}
	c.JSON(http.StatusOK, item)
}

// DecideReviewItem 人工评审结论回写（spec §9 状态机：pending → reviewed）。
func (h *EvaluationHandler) DecideReviewItem(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	if h.review == nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("evaluation review unavailable")))
		return
	}
	actorID, ok := userIDFromCtx(c)
	if !ok || actorID == "" {
		respondMissingUser(c)
		return
	}
	var req gen.ReviewItemDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	item, err := h.review.Decide(c.Request.Context(), tenantID, c.Param("id"), actorID,
		domain.HumanVerdict(req.Verdict), req.Reason)
	if err != nil {
		if errors.Is(err, evalapp.ErrReviewItemNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "review item not found"})
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, item)
}
