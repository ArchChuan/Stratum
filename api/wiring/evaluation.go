package wiring

import (
	"context"
	"encoding/json"
	"errors"
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
	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Evaluation struct {
	Service              *evalapp.Service
	SuiteService         *evalapp.SuiteService
	JobService           *evalapp.JobService
	Worker               *evalapp.Worker
	OptimizationService  *evalapp.OptimizationService
	ExperimentService    *evalapp.ExperimentService
	FeedbackService      *evalapp.FeedbackService
	QueryService         *evalapp.QueryService
	CandidateService     *evalapp.CandidateCommandService
	AgentProvider        evalport.AgentRevisionProvider
	MCPProvider          evalport.ResourceRevisionProvider
	KnowledgeProvider    evalport.ResourceRevisionProvider
	BaselineService      *evalapp.BaselineService
	AgentRevisionApplier evalport.AgentRevisionApplier
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
	ctx context.Context, tenantID, requestedBy string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	adapter, err := r.adapter(ref.Kind)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	return adapter.ExecuteRevision(ctx, tenantID, requestedBy, ref, testCase)
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

type evaluationBaselineRouter struct {
	providers map[evaldomain.ResourceKind]evalport.ResourceRevisionProvider
}

func newEvaluationBaselineService(
	skillProvider evalport.ResourceRevisionProvider,
	agentProvider evalport.ResourceRevisionProvider,
	mcpProvider evalport.ResourceRevisionProvider,
	knowledgeProvider evalport.ResourceRevisionProvider,
) *evalapp.BaselineService {
	providers := make(map[evaldomain.ResourceKind]evalport.ResourceRevisionProvider, 4)
	for kind, provider := range map[evaldomain.ResourceKind]evalport.ResourceRevisionProvider{
		evaldomain.ResourceKindSkill:     skillProvider,
		evaldomain.ResourceKindAgent:     agentProvider,
		evaldomain.ResourceKindMCP:       mcpProvider,
		evaldomain.ResourceKindKnowledge: knowledgeProvider,
	} {
		if provider != nil {
			providers[kind] = provider
		}
	}
	return evalapp.NewBaselineService(evaluationBaselineRouter{providers: providers})
}

func (r evaluationBaselineRouter) CreatePublishedBaseline(
	ctx context.Context, tenantID string, kind evaldomain.ResourceKind, resourceID string,
) (evaldomain.ResourceRef, error) {
	provider := r.providers[kind]
	if provider == nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation baseline provider unavailable for %q", kind)
	}
	return provider.CreatePublishedBaseline(ctx, tenantID, resourceID)
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

type skillEvaluationRepositoryAdapter struct {
	repo evalport.ExperimentRepository
}

func (a skillEvaluationRepositoryAdapter) ResolveSkillEvaluation(
	ctx context.Context, tenantID, skillID string,
) (skillEvaluationStatus, error) {
	deployment, found, err := a.repo.ResolveDeployment(ctx, tenantID, string(evaldomain.ResourceKindSkill), skillID)
	if err != nil || !found || deployment.ExperimentID == "" {
		return skillEvaluationStatus{}, err
	}
	experiment, found, err := a.repo.Get(ctx, tenantID, deployment.ExperimentID)
	if err != nil || !found {
		return skillEvaluationStatus{}, err
	}
	return skillEvaluationStatus{ExperimentID: experiment.ID, Status: string(experiment.Status)}, nil
}

type skillCandidateManager struct {
	versions  *skillapp.VersionService
	revisions evalport.RevisionRepository
}

type experimentSkillRevisionResolver struct {
	service *evalapp.ExperimentService
}

type experimentAgentRevisionResolver struct {
	service *evalapp.ExperimentService
	adapter agentEvaluationAdapter
}

type experimentMCPRevisionResolver struct {
	service *evalapp.ExperimentService
	adapter mcpEvaluationAdapter
}

type experimentKnowledgeRevisionResolver struct {
	service *evalapp.ExperimentService
	adapter knowledgeEvaluationAdapter
}

func (m skillCandidateManager) CreatePublishedBaseline(
	ctx context.Context, tenantID, skillID string,
) (evaldomain.ResourceRef, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(skillID) == "" {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: tenant and skill IDs required")
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin,
	})
	revision, err := m.versions.ResolveActivePublishedRevision(ctx, skillID)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: resolve active baseline: %w", err)
	}
	if m.revisions == nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: revision index unavailable")
	}
	summary, err := m.versions.PublishedRevisionSafeSummary(ctx, skillID, revision.ID)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: summarize baseline: %w", err)
	}
	indexed := evaldomain.ResourceRevision{
		ID: revision.ID, ResourceKind: evaldomain.ResourceKindSkill, ResourceID: skillID,
		Source: evaldomain.RevisionSourceManual, Status: evaldomain.RevisionStatusPublished,
		ContentHash: revision.ContentHash, PayloadRef: "skill://" + revision.ID, PayloadHash: revision.ContentHash,
		SafeSummary: summary, CreatedBy: "evaluation-worker", CreatedAt: time.Now().UTC(),
	}
	if err := indexed.Validate(); err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: validate baseline index: %w", err)
	}
	_, _, err = m.revisions.Create(ctx, tenantID, indexed, "skill-baseline-"+stableTenantFingerprint(
		tenantID, strings.Join([]string{skillID, revision.ID, revision.ContentHash}, "\x00"),
	))
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation Skill adapter: register baseline: %w", err)
	}
	return evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindSkill, ResourceID: skillID, RevisionID: revision.ID,
	}, nil
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

func (r experimentAgentRevisionResolver) ResolveAgentRevision(
	ctx context.Context,
	tenantID, agentID, subjectID string,
) (agentport.AgentRevisionAssignment, bool, error) {
	assignment, found, err := r.service.ResolveAssignment(
		ctx, tenantID, evaldomain.ResourceKindAgent, agentID, subjectID,
	)
	if err != nil || !found {
		return agentport.AgentRevisionAssignment{}, found, err
	}
	_, snapshot, revisionFound, err := r.adapter.get(ctx, tenantID, evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindAgent, ResourceID: agentID, RevisionID: assignment.RevisionID,
	})
	if err != nil {
		return agentport.AgentRevisionAssignment{}, false, err
	}
	if !revisionFound {
		return agentport.AgentRevisionAssignment{}, false, evalport.ErrCenterResourceNotFound
	}
	return agentport.AgentRevisionAssignment{
		Revision: snapshot, RevisionID: assignment.RevisionID,
		ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
	}, true, nil
}

func (r experimentMCPRevisionResolver) ResolveMCPRevision(
	ctx context.Context,
	tenantID, serverID, subjectID string,
) (agentport.MCPRevisionAssignment, bool, error) {
	assignment, found, err := r.service.ResolveAssignment(
		ctx, tenantID, evaldomain.ResourceKindMCP, serverID, subjectID,
	)
	return agentport.MCPRevisionAssignment{
		RevisionID: assignment.RevisionID, ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
	}, found, err
}

func (r experimentMCPRevisionResolver) LoadMCPRuntimeRevision(
	ctx context.Context, tenantID, serverID, revisionID string,
) (mcpRuntimeRevision, error) {
	_, snapshot, err := r.adapter.loadRevision(ctx, tenantID, evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMCP, ResourceID: serverID, RevisionID: revisionID,
	}, true)
	if err != nil {
		return mcpRuntimeRevision{}, err
	}
	config, err := r.adapter.loadRuntimeConfig(ctx, tenantID, snapshot)
	if err != nil {
		return mcpRuntimeRevision{}, err
	}
	config.Timeout = time.Duration(snapshot.TimeoutMS) * time.Millisecond
	if config.Retry == nil {
		config.Retry = &mcpdomain.RetryConfig{}
	}
	config.Retry.Enabled = snapshot.MaxRetries > 0
	config.Retry.MaxRetries = snapshot.MaxRetries
	return mcpRuntimeRevision{
		Config: config, EnabledTools: append([]string(nil), snapshot.EnabledTools...),
		Timeout: config.Timeout, MaxRetries: snapshot.MaxRetries,
	}, nil
}

func (r experimentKnowledgeRevisionResolver) ResolveKnowledgeRevision(
	ctx context.Context, tenantID, workspaceName, subjectID string,
) (agentport.KnowledgeRevisionAssignment, bool, error) {
	assignment, found, err := r.service.ResolveAssignment(
		ctx, tenantID, evaldomain.ResourceKindKnowledge, workspaceName, subjectID,
	)
	if err != nil || !found {
		return agentport.KnowledgeRevisionAssignment{}, found, err
	}
	revision, err := r.LoadKnowledgeRevision(ctx, tenantID, workspaceName, assignment.RevisionID)
	if err != nil {
		return agentport.KnowledgeRevisionAssignment{}, false, err
	}
	return agentport.KnowledgeRevisionAssignment{
		Revision: revision, ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
	}, true, nil
}

func (r experimentKnowledgeRevisionResolver) LoadKnowledgeRevision(
	ctx context.Context, tenantID, workspaceName, revisionID string,
) (agentport.KnowledgeRetrievalRevision, error) {
	_, snapshot, err := r.adapter.loadRevision(ctx, tenantID, evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindKnowledge, ResourceID: workspaceName, RevisionID: revisionID,
	}, true)
	if err != nil {
		return agentport.KnowledgeRetrievalRevision{}, err
	}
	documents, err := r.adapter.source.ListSnapshotDocuments(ctx, tenantID, snapshot.WorkspaceID)
	if err != nil {
		return agentport.KnowledgeRetrievalRevision{}, fmt.Errorf("load Knowledge runtime documents: %w", err)
	}
	documentSetHash, err := knowledgeDocumentSetHash(documents)
	if err != nil {
		return agentport.KnowledgeRetrievalRevision{}, err
	}
	if documentSetHash != snapshot.DocumentSetHash {
		return agentport.KnowledgeRetrievalRevision{}, errors.New("Knowledge runtime document set changed")
	}
	return agentport.KnowledgeRetrievalRevision{
		RevisionID: revisionID, WorkspaceID: snapshot.WorkspaceID, WorkspaceName: snapshot.WorkspaceName,
		EmbeddingModel: snapshot.EmbeddingIdentity, QueryMode: snapshot.QueryMode, TopK: snapshot.TopK,
		ScoreThreshold: snapshot.ScoreThreshold, Reranking: legacyRerankingValue(snapshot.Reranking),
		QueryRewrite: snapshot.QueryRewrite,
	}, nil
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
	version, err := m.versions.ResolveEvaluableRevision(ctx, ref.ResourceID, ref.RevisionID)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	summary, err := m.versions.EvaluableRevisionSafeSummary(ctx, ref.ResourceID, ref.RevisionID)
	if err != nil {
		return evaldomain.ResourceRevision{}, err
	}
	return evaldomain.ResourceRevision{
		ID: version.ID, ResourceKind: evaldomain.ResourceKindSkill, ResourceID: version.SkillID,
		Source: skillRevisionSource(version.Status), Status: skillRevisionStatus(version.Status),
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
	return m.versions.EvaluableRevisionSafeSummary(ctx, ref.ResourceID, ref.RevisionID)
}

func skillRevisionSource(status skilldomain.VersionStatus) evaldomain.RevisionSource {
	if status == skilldomain.VersionStatusCandidate {
		return evaldomain.RevisionSourceOptimization
	}
	return evaldomain.RevisionSourceManual
}

func skillRevisionStatus(status skilldomain.VersionStatus) evaldomain.RevisionStatus {
	if status == skilldomain.VersionStatusPublished {
		return evaldomain.RevisionStatusPublished
	}
	return evaldomain.RevisionStatusDraft
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
	gateway, ok := r.resolver.Resolve(ctx, request.TenantID)
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
		TenantID: request.TenantID,
		Type:     agentport.CapLLM,
		Timeout:  60 * time.Second,
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

func (a agentScenarioEvaluationAdapter) ExecuteRevision(
	ctx context.Context, tenantID, requestedBy string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	if ref.Kind != evaldomain.ResourceKindSkill {
		return evalport.ExecutionResult{}, fmt.Errorf("agent scenario evaluation: unsupported resource kind %q", ref.Kind)
	}
	// Inject tenant context so the agent-context binding port (whose execTenant
	// reads it) routes to the right schema; the raw agent_skill_links read now
	// lives behind agentport.AgentSkillBinding, not here.
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: requestedBy, Role: postgres.RoleTenantAdmin,
	})
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
		agentapp.ExecRequest{Query: query, UserID: requestedBy},
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
		TraceID: evidence.TraceID, UserID: evidence.UserID, CostUSD: evidence.CostUSD, LatencyMs: evidence.LatencyMs,
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
	revisionRepo := evalpersist.NewPgRevisionRepository(db)
	manager := skillCandidateManager{versions: c.Skill.VersionService, revisions: revisionRepo}
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
	var runtimeAgentAdapter *agentEvaluationAdapter
	var mcpProvider evalport.ResourceRevisionProvider
	var runtimeMCPAdapter *mcpEvaluationAdapter
	var knowledgeProvider evalport.ResourceRevisionProvider
	var runtimeKnowledgeAdapter *knowledgeEvaluationAdapter
	var sharedRevisionService *evalapp.RevisionService
	if c.RevisionObjectStore != nil {
		sharedRevisionService = evalapp.NewRevisionService(
			evalpersist.RevisionObjectStoreAdapter{Store: c.RevisionObjectStore},
			revisionRepo,
		)
	}
	if c.Agent != nil && sharedRevisionService != nil {
		agentAdapter := agentEvaluationAdapter{
			revisions: sharedRevisionService, agents: c.Agent.Service, modelValidator: tenantModelValidator(c.Agent.TenantResolver),
			agentUpdater: c.Agent.Service, actorID: "evaluation-worker",
		}
		resourceAdapters[evaldomain.ResourceKindAgent] = agentAdapter
		candidateCreators[evaldomain.ResourceKindAgent] = agentAdapter
		agentProvider = agentAdapter
		runtimeAgentAdapter = &agentAdapter
	}
	if c.MCP != nil && c.MCP.Manager != nil && sharedRevisionService != nil {
		mcpAdapter := mcpEvaluationAdapter{
			runtime: c.MCP.Manager, revisions: sharedRevisionService,
			runtimeStore: c.RevisionObjectStore, actorID: "evaluation-worker",
		}
		resourceAdapters[evaldomain.ResourceKindMCP] = mcpAdapter
		candidateCreators[evaldomain.ResourceKindMCP] = mcpAdapter
		mcpProvider = mcpAdapter
		runtimeMCPAdapter = &mcpAdapter
	}
	if c.Knowledge != nil && c.Knowledge.WorkspaceService != nil && c.Knowledge.RAGService != nil &&
		sharedRevisionService != nil {
		knowledgeAdapter := knowledgeEvaluationAdapter{
			revisions: sharedRevisionService, source: c.Knowledge.WorkspaceService, rerankAvailable: c.Config.RerankConfigured,
			evaluator: knowledgeapp.NewRetrievalEvaluator(c.Knowledge.RAGService), actorID: "evaluation-worker",
		}
		resourceAdapters[evaldomain.ResourceKindKnowledge] = knowledgeAdapter
		candidateCreators[evaldomain.ResourceKindKnowledge] = knowledgeAdapter
		knowledgeProvider = knowledgeAdapter
		runtimeKnowledgeAdapter = &knowledgeAdapter
	}
	var traceReader evalport.TraceEvidenceReader
	if c.Agent != nil {
		traceReader = evaluationTraceEvidenceAdapter{provider: c.Agent.EvidenceProvider}
	}
	service := evalapp.NewService(evaluationResourceRouter{adapters: resourceAdapters}, runRepo, traceReader, suiteRepo)
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
	experimentRunner := evalapp.NewExperimentRunner(experimentService, experimentRepo, feedbackRepo)
	worker := evalapp.NewWorker(
		evaluationTenantLister{pool: db},
		evalapp.NewMultiRunner(jobService, experimentRunner),
		time.Second,
		c.platformMetrics(),
	)
	worker.Start(ctx)
	c.shutdown = append(c.shutdown, func(context.Context) error { worker.Stop(); return nil })
	baselineService := newEvaluationBaselineService(manager, agentProvider, mcpProvider, knowledgeProvider)
	var agentRevisionApplier evalport.AgentRevisionApplier
	if runtimeAgentAdapter != nil {
		agentRevisionApplier = *runtimeAgentAdapter
	}
	c.Evaluation = &Evaluation{
		Service:              service,
		SuiteService:         suiteService,
		JobService:           jobService,
		Worker:               worker,
		OptimizationService:  optimizationService,
		ExperimentService:    experimentService,
		FeedbackService:      feedbackService,
		QueryService:         evalapp.NewQueryService(queryRepo),
		CandidateService:     evalapp.NewCandidateCommandService(candidateRepo),
		AgentProvider:        agentProvider,
		MCPProvider:          mcpProvider,
		KnowledgeProvider:    knowledgeProvider,
		BaselineService:      baselineService,
		AgentRevisionApplier: agentRevisionApplier,
	}
	c.applyAgentRevisionResolvers(experimentService, runtimeAgentAdapter, runtimeMCPAdapter, runtimeKnowledgeAdapter)
	c.applySkillEvaluationReader(experimentRepo)
	return nil
}

func (c *Container) applyAgentRevisionResolvers(
	experimentService *evalapp.ExperimentService,
	runtimeAgentAdapter *agentEvaluationAdapter,
	runtimeMCPAdapter *mcpEvaluationAdapter,
	runtimeKnowledgeAdapter *knowledgeEvaluationAdapter,
) {
	if c.Agent == nil || c.Agent.Service == nil {
		return
	}
	c.Agent.Service.SetSkillRevisionResolver(experimentSkillRevisionResolver{service: experimentService})
	if runtimeAgentAdapter != nil {
		c.Agent.Service.SetAgentRevisionResolver(experimentAgentRevisionResolver{
			service: experimentService, adapter: *runtimeAgentAdapter,
		})
	}
	if runtimeMCPAdapter != nil && c.MCP != nil && c.MCP.Manager != nil {
		resolver := experimentMCPRevisionResolver{service: experimentService, adapter: *runtimeMCPAdapter}
		c.Agent.Service.SetMCPRevisionResolver(resolver)
		c.Agent.Service.SetMCPToolExecutor(agentMCPExecutor{
			clients: c.MCP.Manager, revisionRuntime: c.MCP.Manager, revisions: resolver,
		})
	}
	if runtimeKnowledgeAdapter != nil {
		c.Agent.Service.SetKnowledgeRevisionResolver(experimentKnowledgeRevisionResolver{
			service: experimentService, adapter: *runtimeKnowledgeAdapter,
		})
	}
}

func (c *Container) applySkillEvaluationReader(experimentRepo evalport.ExperimentRepository) {
	if c.Agent == nil || c.Skill == nil || c.Skill.VersionService == nil {
		return
	}
	if diagnostics, ok := c.Agent.DiagnosticProvider.(*systemAssistantDiagnosticAdapter); ok {
		diagnostics.setSkillEvaluationReader(
			c.Skill.VersionService, skillEvaluationRepositoryAdapter{repo: experimentRepo},
			traceAgentBindingResolver{evidence: c.Agent.EvidenceProvider, registry: c.Agent.Registry},
		)
	}
}
