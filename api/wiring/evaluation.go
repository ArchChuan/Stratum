package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentpersist "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	evalpersist "github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Evaluation struct {
	Service             *evalapp.Service
	SuiteService        *evalapp.SuiteService
	JobService          *evalapp.JobService
	Worker              *evalapp.Worker
	OptimizationService *evalapp.OptimizationService
	ExperimentService   *evalapp.ExperimentService
	FeedbackService     *evalapp.FeedbackService
	QueryService        *evalapp.QueryService
	CandidateService    *evalapp.CandidateCommandService
	AgentProvider       evalport.AgentRevisionProvider
}

type evaluationResourceRouter struct {
	adapters map[evaldomain.ResourceKind]evalport.ResourceAdapter
}

func (r evaluationResourceRouter) adapter(kind evaldomain.ResourceKind) (evalport.ResourceAdapter, error) {
	adapter := r.adapters[kind]
	if adapter == nil {
		return nil, fmt.Errorf("evaluation resource adapter unavailable for %q", kind)
	}
	return adapter, nil
}

func (r evaluationResourceRouter) ExecuteRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	adapter, err := r.adapter(ref.Kind)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	return adapter.ExecuteRevision(ctx, tenantID, ref, testCase)
}

func (r evaluationResourceRouter) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	adapter, err := r.adapter(ref.Kind)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	return adapter.ResolveRevision(ctx, tenantID, ref)
}

func (r evaluationResourceRouter) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	adapter, err := r.adapter(ref.Kind)
	if err != nil {
		return nil, err
	}
	return adapter.SafeSummary(ctx, tenantID, ref)
}

type evaluationCandidateRouter struct {
	creators map[evaldomain.ResourceKind]evalport.CandidateCreator
}

func (r evaluationCandidateRouter) creator(kind evaldomain.ResourceKind) (evalport.CandidateCreator, error) {
	creator := r.creators[kind]
	if creator == nil {
		return nil, fmt.Errorf("evaluation candidate creator unavailable for %q", kind)
	}
	return creator, nil
}

func (r evaluationCandidateRouter) LoadOptimizableSnapshot(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef,
) (map[string]any, error) {
	creator, err := r.creator(baseline.Kind)
	if err != nil {
		return nil, err
	}
	return creator.LoadOptimizableSnapshot(ctx, tenantID, baseline)
}

func (r evaluationCandidateRouter) CreateCandidate(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	creator, err := r.creator(baseline.Kind)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	return creator.CreateCandidate(ctx, tenantID, baseline, patch)
}

type skillCandidateManager struct {
	versions *skillapp.VersionService
}

type experimentSkillRevisionResolver struct {
	service *evalapp.ExperimentService
}

func (r experimentSkillRevisionResolver) ResolveSkillRevision(
	ctx context.Context,
	tenantID, skillID, subjectID string,
) (agentport.SkillRevisionAssignment, bool, error) {
	assignment, found, err := r.service.ResolveAssignment(ctx, tenantID, evaldomain.ResourceKindSkill, skillID, subjectID)
	return agentport.SkillRevisionAssignment{
		RevisionID: assignment.RevisionID, ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
	}, found, err
}

func (m skillCandidateManager) LoadOptimizableSnapshot(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef,
) (map[string]any, error) {
	ctx, err := evaluationSkillContext(ctx, tenantID, baseline)
	if err != nil {
		return nil, err
	}
	version, err := m.versions.ResolvePublishedRevision(ctx, baseline.ResourceID, baseline.RevisionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"instructions": version.Instructions,
	}, nil
}

func (m skillCandidateManager) CreateCandidate(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	ctx, err := evaluationSkillContext(ctx, tenantID, baseline)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	if _, err := m.versions.ResolvePublishedRevision(ctx, baseline.ResourceID, baseline.RevisionID); err != nil {
		return evaldomain.ResourceRef{}, err
	}
	version, err := m.versions.CreateCandidate(ctx, baseline.ResourceID, baseline.RevisionID, skillapp.CandidateInput{
		Source: patch.Source, PromptPatch: patch.PromptPatch,
		GenerationMetadata: map[string]any{"rationale": patch.Rationale},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	return evaldomain.ResourceRef{
		Kind: baseline.Kind, ResourceID: baseline.ResourceID, RevisionID: version.ID,
	}, nil
}

func (m skillCandidateManager) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	ctx, err := evaluationSkillContext(ctx, tenantID, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	version, err := m.versions.ResolvePublishedRevision(ctx, ref.ResourceID, ref.RevisionID)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	summary, err := m.versions.PublishedRevisionSafeSummary(ctx, ref.ResourceID, ref.RevisionID)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	return evaldomain.ResourceRevision{
		ID: version.ID, ResourceKind: evaldomain.ResourceKindSkill, ResourceID: version.SkillID,
		Source: evaldomain.RevisionSourceManual, Status: evaldomain.RevisionStatusPublished,
		ContentHash: version.ContentHash, PayloadRef: "skill://" + version.ID, PayloadHash: version.ContentHash,
		SafeSummary: summary,
	}, nil
}

func (m skillCandidateManager) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	ctx, err := evaluationSkillContext(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return m.versions.PublishedRevisionSafeSummary(ctx, ref.ResourceID, ref.RevisionID)
}

func evaluationSkillContext(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (context.Context, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("evaluation Skill adapter: tenant ID required")
	}
	if ref.Kind != evaldomain.ResourceKindSkill {
		return nil, fmt.Errorf("evaluation Skill adapter: unsupported resource kind %q", ref.Kind)
	}
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("evaluation Skill adapter: %w", err)
	}
	return postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin,
	}), nil
}

type gatewayPromptRewriter struct {
	resolver agentport.TenantCapabilityResolver
}

func (r gatewayPromptRewriter) Rewrite(
	ctx context.Context, request evalapp.PromptRewriteRequest,
) ([]evaldomain.CandidatePatch, error) {
	gateway, keys, ok := r.resolver.Resolve(ctx, request.TenantID)
	if !ok || gateway == nil {
		return nil, fmt.Errorf("prompt optimizer: tenant has no LLM provider configured")
	}
	snapshotJSON, err := json.Marshal(request.BaselineSnapshot)
	if err != nil {
		return nil, err
	}
	failuresJSON, err := json.Marshal(request.FailureSummaries)
	if err != nil {
		return nil, err
	}
	response, err := gateway.Route(ctx, agentport.CapabilityRequest{
		TenantID:   request.TenantID,
		Type:       agentport.CapLLM,
		LLMAPIKeys: keys,
		Timeout:    60 * time.Second,
		LLM: &agentport.LLMCapRequest{
			Model: "qwen-plus", Temperature: 0.2, MaxTokens: 2048,
			Messages: []agentport.LLMMessage{
				{Role: "system", Content: "你是提示词优化器。只生成候选内容，不决定发布。仅输出 JSON 数组。"},
				{Role: "user", Content: fmt.Sprintf(
					"基线配置：%s\n失败摘要：%s\n输出最多3项，每项格式：{\"prompt_patch\":{\"instructions\":\"...\"},\"rationale\":\"...\"}。不得修改 requirements、权限、密钥或网络配置。",
					string(snapshotJSON), string(failuresJSON),
				)},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return parsePromptRewritePatches(response.Content)
}

func parsePromptRewritePatches(content string) ([]evaldomain.CandidatePatch, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var patches []evaldomain.CandidatePatch
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &patches); err != nil {
		return nil, fmt.Errorf("prompt optimizer: parse candidate patches: %w", err)
	}
	if len(patches) == 0 || len(patches) > 3 {
		return nil, fmt.Errorf("prompt optimizer: expected 1-3 candidate patches")
	}
	for i := range patches {
		if err := evaldomain.ValidatePromptPatch(patches[i].PromptPatch); err != nil {
			return nil, err
		}
		patches[i].Source = "llm_rewrite"
	}
	return patches, nil
}

type evaluationTenantLister struct {
	pool *pgxpool.Pool
}

type agentScenarioEvaluationAdapter struct {
	agents    *agentapp.AgentService
	skills    agentport.SkillActivationResolver
	bindings  agentport.AgentSkillBinding
	resources skillCandidateManager
}

func (a agentScenarioEvaluationAdapter) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	return a.resources.ResolveRevision(ctx, tenantID, ref)
}

func (a agentScenarioEvaluationAdapter) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	return a.resources.SafeSummary(ctx, tenantID, ref)
}

func (a agentScenarioEvaluationAdapter) ExecuteRevision(ctx context.Context, tenantID string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase) (evalport.ExecutionResult, error) {
	if ref.Kind != evaldomain.ResourceKindSkill {
		return evalport.ExecutionResult{}, fmt.Errorf("agent scenario evaluation: unsupported resource kind %q", ref.Kind)
	}
	// Inject tenant context so the agent-context binding port (whose execTenant
	// reads it) routes to the right schema; the raw agent_skill_links read now
	// lives behind agentport.AgentSkillBinding, not here.
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin})
	agentID, found, err := a.bindings.FindAgentBySkill(ctx, ref.ResourceID)
	if err != nil {
		return evalport.ExecutionResult{}, fmt.Errorf("agent scenario evaluation: resolve agent for Skill %s: %w", ref.ResourceID, err)
	}
	if !found {
		return evalport.ExecutionResult{}, fmt.Errorf("agent scenario evaluation requires an Agent bound to Skill %s", ref.ResourceID)
	}
	catalog, err := a.skills.ResolveSkills(ctx, tenantID, []agentport.SkillRevisionRef{{SkillID: ref.ResourceID, RevisionID: ref.RevisionID}})
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	activation, ok := catalog[ref.ResourceID]
	if !ok {
		return evalport.ExecutionResult{}, fmt.Errorf("Skill revision %s is not available", ref.RevisionID)
	}
	queryBytes, err := json.Marshal(testCase.Input)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	query := string(queryBytes)
	if text, ok := testCase.Input.(string); ok {
		query = text
	}
	traceID := uuid.Must(uuid.NewV7()).String()
	result, duration, err := a.agents.ExecuteSkillScenario(
		ctx,
		agentID,
		agentapp.ExecRequest{Query: query, UserID: "evaluation-worker"},
		agentapp.ExecMeta{
			TenantID: tenantID,
			TraceID:  traceID,
			EvolutionTrace: agentapp.EvolutionTraceMetadata{
				Evaluation: true,
				ResourceManifest: map[string]string{
					"skill:" + ref.ResourceID: ref.RevisionID,
				},
			},
		},
		activation,
	)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	return evalport.ExecutionResult{Output: result.Output, TraceID: traceID, Tokens: result.TokensUsed, CostUSD: result.CostUSD, DurationMs: duration}, nil
}

func (l evaluationTenantLister) ListTenantIDs(ctx context.Context) ([]string, error) {
	schemas, err := postgres.ListTenantSchemas(ctx, l.pool)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		ids = append(ids, strings.TrimPrefix(schema, "tenant_"))
	}
	return ids, nil
}

type evaluationTraceEvidenceAdapter struct {
	provider agentport.TraceEvidenceProvider
}

func (a evaluationTraceEvidenceAdapter) Resolve(
	ctx context.Context, tenantID, traceID string,
) (evalport.ObservedTrace, error) {
	evidence, err := a.provider.Resolve(ctx, tenantID, traceID)
	if err != nil {
		return evalport.ObservedTrace{}, err
	}
	return mapEvaluationEvidence(evidence), nil
}

func (a evaluationTraceEvidenceAdapter) ResolveBatch(
	ctx context.Context, tenantID string, traceIDs []string,
) (map[string]evalport.ObservedTrace, error) {
	evidence, err := a.provider.ResolveBatch(ctx, tenantID, traceIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]evalport.ObservedTrace, len(evidence))
	for traceID, trace := range evidence {
		out[traceID] = mapEvaluationEvidence(trace)
	}
	return out, nil
}

func mapEvaluationEvidence(evidence agentdomain.TraceEvidence) evalport.ObservedTrace {
	assignments := make(map[string]evalport.ObservedResourceAssignment, len(evidence.ResourceAssignments))
	for resource, assignment := range evidence.ResourceAssignments {
		assignments[resource] = evalport.ObservedResourceAssignment{
			RevisionID: assignment.RevisionID, ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
		}
	}
	return evalport.ObservedTrace{
		TraceID: evidence.TraceID, CostUSD: evidence.CostUSD, LatencyMs: evidence.LatencyMs,
		Success: evidence.Status == agentdomain.ExecStatusSuccess, SecurityViolation: evidence.SecurityViolation,
		Assignments: assignments,
	}
}

func (c *Container) buildEvaluation(ctx context.Context) error {
	db := c.dbOrNil()
	if db == nil || c.Skill == nil || c.Skill.VersionService == nil || c.Agent == nil || c.Agent.Service == nil {
		c.Evaluation = &Evaluation{}
		return nil
	}
	suiteRepo := evalpersist.NewPgSuiteRepository(db)
	runRepo := evalpersist.NewPgRunRepository(db)
	jobRepo := evalpersist.NewPgJobRepository(db)
	optimizationRepo := evalpersist.NewPgOptimizationRepository(db)
	experimentRepo := evalpersist.NewPgExperimentRepository(db)
	feedbackRepo := evalpersist.NewPgFeedbackRepository(db)
	queryRepo := evalpersist.NewPgCenterQueryRepository(db)
	candidateRepo := evalpersist.NewPgCandidateCommandRepository(db)
	suiteService := evalapp.NewSuiteService(suiteRepo)
	activationResolver := publishedSkillActivationResolver{versions: c.Skill.VersionService}
	manager := skillCandidateManager{versions: c.Skill.VersionService}
	skillAdapter := agentScenarioEvaluationAdapter{
		agents:    c.Agent.Service,
		skills:    activationResolver,
		bindings:  agentpersist.NewPgAgentRepo(db),
		resources: manager,
	}
	resourceAdapters := map[evaldomain.ResourceKind]evalport.ResourceAdapter{
		evaldomain.ResourceKindSkill: skillAdapter,
	}
	candidateCreators := map[evaldomain.ResourceKind]evalport.CandidateCreator{
		evaldomain.ResourceKindSkill: manager,
	}
	var agentProvider evalport.AgentRevisionProvider
	if c.Agent != nil && c.Agent.RevisionObjectStore != nil {
		revisionService := evalapp.NewRevisionService(
			evalpersist.RevisionObjectStoreAdapter{Store: c.Agent.RevisionObjectStore},
			evalpersist.NewPgRevisionRepository(db),
		)
		agentAdapter := agentEvaluationAdapter{
			revisions: revisionService, agents: c.Agent.Service, actorID: "evaluation-worker",
		}
		resourceAdapters[evaldomain.ResourceKindAgent] = agentAdapter
		candidateCreators[evaldomain.ResourceKindAgent] = agentAdapter
		agentProvider = agentAdapter
	}
	service := evalapp.NewService(evaluationResourceRouter{adapters: resourceAdapters}, runRepo, suiteRepo)
	jobService := evalapp.NewJobService(jobRepo, service)
	var rewriter evalapp.PromptRewriter
	if c.Agent != nil && c.Agent.TenantResolver != nil {
		rewriter = gatewayPromptRewriter{resolver: c.Agent.TenantResolver}
	}
	optimizationService := evalapp.NewOptimizationService(
		evaluationCandidateRouter{creators: candidateCreators}, rewriter, optimizationRepo,
	)
	experimentService := evalapp.NewExperimentService(experimentRepo)
	feedbackService := evalapp.NewFeedbackService(
		feedbackRepo, experimentService, evaluationTraceEvidenceAdapter{provider: c.Agent.EvidenceProvider},
	)
	worker := evalapp.NewWorker(evaluationTenantLister{pool: db}, jobService, time.Second)
	worker.Start(ctx)
	c.shutdown = append(c.shutdown, func(context.Context) error { worker.Stop(); return nil })
	c.Evaluation = &Evaluation{
		Service:             service,
		SuiteService:        suiteService,
		JobService:          jobService,
		Worker:              worker,
		OptimizationService: optimizationService,
		ExperimentService:   experimentService,
		FeedbackService:     feedbackService,
		QueryService:        evalapp.NewQueryService(queryRepo),
		CandidateService:    evalapp.NewCandidateCommandService(candidateRepo),
		AgentProvider:       agentProvider,
	}
	if c.Agent != nil && c.Agent.Service != nil {
		c.Agent.Service.SetSkillRevisionResolver(experimentSkillRevisionResolver{service: experimentService})
	}
	return nil
}
