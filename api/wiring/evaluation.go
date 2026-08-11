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
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	parametersapp "github.com/byteBuilderX/stratum/internal/parameters/application"
	promptapp "github.com/byteBuilderX/stratum/internal/prompt/application"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
	TestCaseGenerator    *evalapp.TestCaseGenerator
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
	// params feeds evaluation.optimizer.* platform parameters into the
	// rewrite request (model / temperature / max_tokens), replacing the
	// legacy hard-coded qwen-plus/0.2/2048. nil degrades to those defaults
	// (db unavailable); a read failure also keeps the definition defaults.
	params *parametersapp.Service
}

// optimizerLLM picks the effective optimizer LLM spec from platform
// parameters, falling back to the definition defaults.
func (r gatewayPromptRewriter) optimizerLLM(
	ctx context.Context,
) (model string, temperature float32, maxTokens int) {
	model, temperature, maxTokens = "qwen-plus", 0.2, 2048
	if r.params == nil {
		return model, temperature, maxTokens
	}
	values, err := r.params.PlatformValues(ctx)
	if err != nil {
		return model, temperature, maxTokens
	}
	if v, ok := values["evaluation.optimizer.model"].(string); ok && v != "" {
		model = v
	}
	if v, ok := values["evaluation.optimizer.temperature"].(float64); ok {
		temperature = float32(v)
	}
	switch v := values["evaluation.optimizer.max_tokens"].(type) {
	case float64:
		maxTokens = int(v)
	case int64:
		maxTokens = int(v)
	}
	return model, temperature, maxTokens
}

func (r gatewayPromptRewriter) Rewrite(
	ctx context.Context, request evalapp.PromptRewriteRequest,
) ([]evaldomain.CandidatePatch, error) {
	gateway, ok := r.resolver.Resolve(ctx, request.TenantID)
	if !ok || gateway == nil {
		return nil, fmt.Errorf("prompt optimizer: tenant has no LLM provider configured")
	}
	model, temperature, maxTokens := r.optimizerLLM(ctx)
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
			Model: model, Temperature: temperature, MaxTokens: maxTokens,
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

// judgeRubricKey is the prompt-registry key for the LLM judge rubric. A
// published global template wins over the built-in default; tenants may
// override it through the prompt registry's tenant scoping.
const judgeRubricKey = "evaluation.judge.rubric"

// judgeDefaultRubric is the built-in rubric used when no global template is
// published. It asks for a binary verdict with a short justification.
const judgeDefaultRubric = `你是一名严谨的评测法官。根据以下标准判断实际输出是否通过：
1. 实际输出是否直接、完整地回答了输入要求；
2. 与期望输出的一致性（期望输出为 null 或空时忽略该项）；
3. 是否存在明显的事实错误或逻辑矛盾。
只输出 JSON：{"passed": true 或 false, "reason": "一句话理由"}。`

// buildEvaluationJudge wires the optional LLM judge. It degrades to a
// disabled judge when the gateway is unavailable (db not configured),
// keeping rule assertions working.
func buildEvaluationJudge(c *Container) evalport.LLMJudge {
	if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
		return nil
	}
	var prompts *promptapp.RegistryService
	if c.Prompt != nil {
		prompts = c.Prompt.Registry
	}
	return judgeAdapter{
		completer: c.LLMGateway.Gateway,
		params:    c.Parameters.Service,
		prompts:   prompts,
	}
}

// judgeAdapter implements evalport.LLMJudge over llmgateway's LLMCompleter.
// The runtime switch and model/temperature come from platform parameters;
// the rubric comes from the prompt registry (global template) unless the
// case declares one explicitly. nil dependencies degrade conservatively:
// disabled judge and built-in defaults, never a silent pass.
type judgeAdapter struct {
	completer llmgatewaydomain.LLMCompleter
	params    *parametersapp.Service
	prompts   *promptapp.RegistryService
}

// Enabled reports the evaluation.judge.enabled platform parameter. Fail
// closed when the parameters service is unavailable.
func (j judgeAdapter) Enabled(ctx context.Context) bool {
	if j.params == nil {
		return false
	}
	values, err := j.params.PlatformValues(ctx)
	if err != nil {
		return false
	}
	enabled, _ := values["evaluation.judge.enabled"].(bool)
	return enabled
}

func (j judgeAdapter) judgeModel(ctx context.Context, requested string) string {
	if requested != "" {
		return requested
	}
	if j.params == nil {
		return "qwen-plus"
	}
	values, err := j.params.PlatformValues(ctx)
	if err != nil {
		return "qwen-plus"
	}
	if model, ok := values["evaluation.judge.model"].(string); ok && model != "" {
		return model
	}
	return "qwen-plus"
}

func (j judgeAdapter) judgeTemperature(ctx context.Context) float32 {
	if j.params == nil {
		return 0
	}
	values, err := j.params.PlatformValues(ctx)
	if err != nil {
		return 0
	}
	if temperature, ok := values["evaluation.judge.temperature"].(float64); ok {
		return float32(temperature)
	}
	return 0
}

func (j judgeAdapter) judgeRubric(ctx context.Context, requested string) string {
	if requested != "" {
		return requested
	}
	if j.prompts != nil {
		if rubric, err := j.prompts.GetEffectivePrompt(ctx, judgeRubricKey, "", "", ""); err == nil && rubric != "" {
			return rubric
		}
	}
	return judgeDefaultRubric
}

func (j judgeAdapter) Judge(ctx context.Context, req evalport.JudgeRequest) (evaldomain.AssertionResult, error) {
	if j.completer == nil {
		return evaldomain.AssertionResult{}, errors.New("LLM judge: no LLM completer configured")
	}
	response, err := j.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:       j.judgeModel(ctx, req.Model),
		Temperature: j.judgeTemperature(ctx),
		MaxTokens:   constants.JudgeMaxTokens,
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: "你是评测法官。只输出 JSON，不输出其他内容。"},
			{Role: "user", Content: fmt.Sprintf(
				"Rubric:\n%s\n\nInput:\n%s\n\nExpected output:\n%s\n\nActual output:\n%s",
				j.judgeRubric(ctx, req.Rubric), req.Input, req.ExpectedOutput, req.Actual,
			)},
		},
	})
	if err != nil {
		return evaldomain.AssertionResult{}, fmt.Errorf("LLM judge: %w", err)
	}
	return parseJudgeResponse(response.Content)
}

// buildEvaluationCaseGen wires the LLM-backed eval-case generator. Returns
// nil when the gateway is unavailable; TestCaseGenerator rejects the whole
// request then instead of silently producing no cases.
// buildEvaluationRuntime resolves the baseline service and agent revision
// applier used by the runtime evaluation entry points.
func buildEvaluationRuntime(
	manager skillCandidateManager,
	agentProvider, mcpProvider, knowledgeProvider evalport.ResourceRevisionProvider,
	runtimeAgentAdapter *agentEvaluationAdapter,
) (*evalapp.BaselineService, evalport.AgentRevisionApplier) {
	baseline := newEvaluationBaselineService(manager, agentProvider, mcpProvider, knowledgeProvider)
	var applier evalport.AgentRevisionApplier
	if runtimeAgentAdapter != nil {
		applier = *runtimeAgentAdapter
	}
	return baseline, applier
}

// buildTestCaseGenerator wires sample source, LLM generator and suite repo
// into the eval-case generation service.
func buildTestCaseGenerator(c *Container, suites evalport.SuiteRepository, db *pgxpool.Pool) *evalapp.TestCaseGenerator {
	return evalapp.NewTestCaseGenerator(
		evalpersist.NewPgCaseSampleSource(db),
		buildEvaluationCaseGen(c),
		suites,
	)
}

func buildEvaluationCaseGen(c *Container) evalport.CaseGenerator {
	if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
		return nil
	}
	var params *parametersapp.Service
	if c.Parameters != nil {
		params = c.Parameters.Service
	}
	return casegenAdapter{completer: c.LLMGateway.Gateway, params: params}
}

// casegenAdapter implements evalport.CaseGenerator over llmgateway's
// LLMCompleter, the same channel as the LLM judge. The model falls back to
// the platform optimizer model; a generation failure returns a
// Valid=false GeneratedCase so the caller can report the per-sample reason
// without aborting the whole pass.
type casegenAdapter struct {
	completer llmgatewaydomain.LLMCompleter
	params    *parametersapp.Service
}

const caseGenSystemPrompt = `你是一名评测用例生成器。给定一条真实生产交互样本（用户查询、实际回答、用户反馈信号），生成一条评测用例。
规则：
1. input：保留或轻度改写原用户查询，不得改变语义；
2. expected_output：基于实际回答推导期望输出；回答明显错误时可给出修正后的期望；
3. assertion_mode：从 exact（精确匹配）、contains（包含）、regex（正则）、judge（LLM 判断）中选择最能验证该意图的模式；
4. reason：一句话说明该用例的来源与生成依据。
只输出 JSON：{"name": "...", "input": ..., "expected_output": ..., "assertion_mode": "...", "reason": "..."}，不要输出其他内容。`

func (a casegenAdapter) Generate(ctx context.Context, req evalport.CaseGenRequest) (evaldomain.GeneratedCase, error) {
	if a.completer == nil {
		return evaldomain.GeneratedCase{Valid: false, Reason: "case generator: no LLM completer configured"}, nil
	}
	response, err := a.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:       a.genModel(ctx),
		Temperature: 0.2,
		MaxTokens:   constants.CaseGenMaxTokens,
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: caseGenSystemPrompt},
			{Role: "user", Content: caseGenUserContent(req)},
		},
	})
	if err != nil {
		return evaldomain.GeneratedCase{}, fmt.Errorf("case generator: %w", err)
	}
	return parseCaseGenResponse(response.Content)
}

func (a casegenAdapter) genModel(ctx context.Context) string {
	if a.params != nil {
		if values, err := a.params.PlatformValues(ctx); err == nil {
			if model, ok := values["evaluation.optimizer.model"].(string); ok && model != "" {
				return model
			}
		}
	}
	return "qwen-plus"
}

// caseGenUserContent renders one sample for the generator, including the
// feedback signal when present.
func caseGenUserContent(req evalport.CaseGenRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "资源类型：%s\n", req.ResourceKind)
	fmt.Fprintf(&b, "用户查询：%s\n", req.Sample.Query)
	fmt.Fprintf(&b, "实际回答：%s\n", req.Sample.Response)
	if req.Sample.Score != nil {
		fmt.Fprintf(&b, "反馈分数：%.2f\n", *req.Sample.Score)
	} else {
		b.WriteString("反馈分数：无\n")
	}
	if len(req.Sample.Outcome) > 0 {
		outcomeJSON, _ := json.Marshal(req.Sample.Outcome)
		fmt.Fprintf(&b, "反馈标签：%s\n", outcomeJSON)
	}
	return b.String()
}

// parseCaseGenResponse extracts one eval-case JSON from the generator
// output, tolerating a markdown code fence. Unparseable output becomes a
// Valid=false GeneratedCase: the sample is rejected with a reason, never
// silently dropped.
func parseCaseGenResponse(content string) (evaldomain.GeneratedCase, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var generated struct {
		Name           string                   `json:"name"`
		Input          any                      `json:"input"`
		ExpectedOutput any                      `json:"expected_output"`
		AssertionMode  evaldomain.AssertionMode `json:"assertion_mode"`
		Reason         string                   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &generated); err != nil {
		return evaldomain.GeneratedCase{}, fmt.Errorf("case generator: parse generated case: %w", err)
	}
	return evaldomain.GeneratedCase{
		Name:           generated.Name,
		Input:          generated.Input,
		ExpectedOutput: generated.ExpectedOutput,
		AssertionMode:  generated.AssertionMode,
		GenerateReason: generated.Reason,
		Valid:          true,
	}, nil
}

// parseJudgeResponse extracts {"passed": bool, "reason": string} from the
// judge output, tolerating a markdown code fence around the JSON.
func parseJudgeResponse(content string) (evaldomain.AssertionResult, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	var verdict struct {
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed)), &verdict); err != nil {
		return evaldomain.AssertionResult{}, fmt.Errorf("LLM judge: parse verdict: %w", err)
	}
	return evaldomain.AssertionResult{Passed: verdict.Passed, Message: verdict.Reason}, nil
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
		[]agentport.SkillActivation{activation},
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
			agentUpdater: c.Agent.Service, actorID: "evaluation-worker", parameters: c.Parameters.Registry,
		}
		resourceAdapters[evaldomain.ResourceKindAgent] = agentAdapter
		candidateCreators[evaldomain.ResourceKindAgent] = agentAdapter
		agentProvider = agentAdapter
		runtimeAgentAdapter = &agentAdapter
	}
	if c.MCP != nil && c.MCP.Manager != nil && sharedRevisionService != nil {
		mcpAdapter := mcpEvaluationAdapter{
			runtime: c.MCP.Manager, revisions: sharedRevisionService,
			runtimeStore: c.RevisionObjectStore, actorID: "evaluation-worker", parameters: c.Parameters.Registry,
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
			evaluator: knowledgeapp.NewRetrievalEvaluator(c.Knowledge.RAGService), actorID: "evaluation-worker", parameters: c.Parameters.Registry,
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
	service := evalapp.NewService(evaluationResourceRouter{adapters: resourceAdapters}, runRepo, traceReader, buildEvaluationJudge(c), suiteRepo)
	jobService := evalapp.NewJobService(jobRepo, service)
	var rewriter evalapp.PromptRewriter
	if c.Agent != nil && c.Agent.TenantResolver != nil {
		rewriter = gatewayPromptRewriter{resolver: c.Agent.TenantResolver, params: c.Parameters.Service}
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
	baselineService, agentRevisionApplier := buildEvaluationRuntime(manager, agentProvider, mcpProvider, knowledgeProvider, runtimeAgentAdapter)
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
		TestCaseGenerator:    buildTestCaseGenerator(c, suiteRepo, db),
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
