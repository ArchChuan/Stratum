package wiring

import (
	"context"
	"fmt"
	"strings"
	"time"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	agentobjects "github.com/byteBuilderX/stratum/internal/agent/infrastructure/objectstore"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
	agentopik "github.com/byteBuilderX/stratum/internal/agent/infrastructure/opik"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	memapp "github.com/byteBuilderX/stratum/internal/memory/application"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// agentCheckpointTenantLister adapts *pgxpool.Pool to the
// func(ctx) ([]string, error) signature consumed by
// CheckpointCleanupWorker.
type agentCheckpointTenantLister struct {
	pool *pgxpool.Pool
}

func (l agentCheckpointTenantLister) list(ctx context.Context) ([]string, error) {
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

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time. Service is the orchestration façade handlers consume.
type Agent struct {
	Registry             *agent.Registry
	Service              *agent.AgentService
	ChatStore            agent.ChatStore
	EvidenceProvider     agentport.TraceEvidenceProvider
	TracePayloadStore    agentport.TracePayloadStore
	RevisionObjectStore  pkgobjectstore.Store
	CheckpointStore      agent.CheckpointStore
	CheckpointCleanup    *agent.CheckpointCleanupWorker
	ApprovalStore        agentport.ToolApprovalRepo
	ApprovalService      *agent.ToolApprovalService
	TenantResolver       agentport.TenantCapabilityResolver
	SkillLookup          agentport.SkillLookup
	DiagnosticProvider   agentport.DiagnosticEvidenceProvider
	ProposalService      *agent.ResourceChangeProposalService
	OperationGateService *agent.OperationGateService
	OperationProposalSvc *agent.OperationProposalService
	PromptResolver       *agent.PromptResolver
}

// ragSearchAdapter wraps *knowledge.RAGService to satisfy
// agentport.RAGSearchProvider. Lives in wiring (the composition root)
// so neither agent/application nor knowledge/application has to know
// about the other.
type ragSearchAdapter struct {
	rag *knowledge.RAGService
}

type tenantMemberRoleService interface {
	GetMemberRole(ctx context.Context, tenantID, userID string) (string, error)
}

type agentToolUserScopeResolver struct {
	members tenantMemberRoleService
}

func (r agentToolUserScopeResolver) ResolveToolUserScope(
	ctx context.Context,
	tenantID, userID, _, _ string,
) (agentport.ToolUserScope, error) {
	if r.members == nil {
		return agentport.ToolUserScope{}, fmt.Errorf("resolve agent tool user scope: tenant membership service unavailable")
	}
	role, err := r.members.GetMemberRole(ctx, tenantID, userID)
	if err != nil {
		return agentport.ToolUserScope{}, fmt.Errorf("resolve agent tool user scope: %w", err)
	}
	switch role {
	case "member", "admin", "owner":
		return agentport.ToolUserScope{UserActive: true, AllowsTool: true}, nil
	default:
		return agentport.ToolUserScope{}, fmt.Errorf("resolve agent tool user scope: unsupported tenant role")
	}
}

func (a ragSearchAdapter) SearchKnowledge(
	ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int,
) (string, error) {
	return knowledge.NewRAGSearchFn(a.rag, tenantID)(ctx, workspaceIDs, query, topK)
}

func (a ragSearchAdapter) SearchKnowledgeRevision(
	ctx context.Context, tenantID string, revision agentport.KnowledgeRetrievalRevision, query string,
) (string, error) {
	if a.rag == nil {
		return "", fmt.Errorf("Knowledge revision search: RAG service unavailable")
	}
	ctx = reqctx.WithTenantID(ctx, tenantID)
	return knowledge.NewRetrievalEvaluator(a.rag).RetrieveContext(ctx, knowledge.RetrievalSnapshot{
		WorkspaceID: revision.WorkspaceID, WorkspaceName: revision.WorkspaceName,
		EmbeddingModel: revision.EmbeddingModel, QueryMode: revision.QueryMode, TopK: revision.TopK,
		ScoreThreshold: revision.ScoreThreshold, Reranking: revision.Reranking,
		QueryRewrite: revision.QueryRewrite,
	}, query)
}

// skillVersionService returns the wired skill VersionService, or nil when the
// skill context was built without a database. The resolver treats a nil
// service as an empty catalog, so agent construction never panics on it.
func skillVersionService(c *Container) *skillapp.VersionService {
	if c.Skill == nil {
		return nil
	}
	return c.Skill.VersionService
}

func tenantMemberService(c *Container) tenantMemberRoleService {
	if c.IAM == nil {
		return nil
	}
	return c.IAM.TenantService
}

// publishedSkillActivationResolver adapts skill/application's context-neutral
// VersionService.ResolveActivation onto agentport.SkillActivationResolver.
// The activation query (active-revision fallback, published/candidate status
// filter, contract name/description fallback) lives in the skill context; the
// composition root only maps the returned view onto the agent port's shape.
type publishedSkillActivationResolver struct {
	versions *skillapp.VersionService
}

func (r publishedSkillActivationResolver) ResolveSkills(
	ctx context.Context, _ string, refs []agentport.SkillRevisionRef,
) (map[string]agentport.SkillActivation, error) {
	catalog := make(map[string]agentport.SkillActivation, len(refs))
	if r.versions == nil || len(refs) == 0 {
		return catalog, nil
	}
	for _, ref := range refs {
		view, found, err := r.versions.ResolveActivation(ctx, ref.SkillID, ref.RevisionID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		catalog[view.SkillID] = agentport.SkillActivation{
			SkillID:               view.SkillID,
			RevisionID:            view.RevisionID,
			Name:                  view.Name,
			Description:           view.Description,
			Instructions:          view.Instructions,
			InputSchema:           view.InputSchema,
			OutputSchema:          view.OutputSchema,
			MCPToolIDs:            view.MCPToolIDs,
			KnowledgeWorkspaceIDs: view.KnowledgeWorkspaceIDs,
			MemoryScopes:          view.MemoryScopes,
		}
	}
	return catalog, nil
}

func (c *Container) buildAgent(ctx context.Context) error {
	db := c.dbOrNil()

	systemAssistantProfile := agent.BuiltinSystemAssistantProfileSource()
	var repo agentport.AgentRepo
	if db != nil {
		repo = persistence.NewPgAgentRepo(db)
	}
	registry := agent.NewRegistry(repo, systemAssistantProfile, c.Logger)
	if c.Memory != nil && c.Memory.Injector != nil {
		registry.SetMemoryInjector(c.Memory.Injector)
	}
	if c.Config.GlobalAgentSystemPrompt != "" {
		registry.SetGlobalSystemSuffix(c.Config.GlobalAgentSystemPrompt)
	}
	if c.Memory != nil && c.Memory.RecallFn != nil {
		registry.SetRecallMemoryFn(c.Memory.RecallFn)
	}

	evidenceProvider := agentopik.NewClient(agentopik.Config{
		BaseURL: c.Config.Opik.URL, Project: c.Config.Opik.Project, Workspace: c.Config.Opik.Workspace,
		APIKey: c.Config.Opik.APIKey, Timeout: c.Config.Opik.Timeout,
	})
	a := &Agent{Registry: registry, EvidenceProvider: evidenceProvider}
	if c.Config.TracePayload.Enabled {
		store := agentobjects.NewStore(
			c.revisionObjectClient, c.Config.TracePayload.Bucket, c.Platform.AESKey,
		)
		a.TracePayloadStore = store
		a.RevisionObjectStore = c.RevisionObjectStore
	}
	if db != nil {
		a.ChatStore = persistence.NewPgChatStore(db, c.Logger)
		a.CheckpointStore = persistence.NewPgCheckpointStore(db)
		a.ApprovalStore = persistence.NewPgToolApprovalStore(db)
		a.ApprovalService = agent.NewToolApprovalService(a.ApprovalStore, a.CheckpointStore, c.Platform.AESKey)
		a.CheckpointCleanup = agent.NewCheckpointCleanupWorker(
			agentCheckpointTenantLister{pool: db}.list,
			a.CheckpointStore,
			10*time.Minute,
			c.Logger,
			c.platformMetrics(),
		)
		a.CheckpointCleanup.Start(ctx)
		c.shutdown = append(c.shutdown, func(context.Context) error {
			a.CheckpointCleanup.Stop()
			return nil
		})
		a.SkillLookup = persistence.NewPgSkillLookup(db)
		var registry *llmgateway.ModelRegistry
		var gw *llmgateway.Gateway
		if c.LLMGateway != nil {
			registry, gw = c.LLMGateway.Registry, c.LLMGateway.Gateway
		}
		a.TenantResolver = newTenantCapabilityResolver(
			registry, gw, c.Logger,
		)
	}

	deps := agent.AgentServiceDeps{
		Registry:                registry,
		SkillLookup:             a.SkillLookup,
		SkillActivationResolver: publishedSkillActivationResolver{versions: skillVersionService(c)},
		TenantResolver:          a.TenantResolver,
		TenantModelValidator:    tenantModelValidator(a.TenantResolver),
		TenantModelCatalog:      tenantModelCatalog(a.TenantResolver),
		ModelContextProvider:    modelContextProvider(a.TenantResolver),
		HistoryCompactorFactory: func(gw agentport.CapabilityGateway, model string, logger *zap.Logger, compactionMaxTokens int) agentport.HistoryCompactor {
			return capgateway.NewLLMHistoryCompactor(gw, model, logger, compactionMaxTokens)
		},
		ChatStore:         a.ChatStore,
		EvidenceProvider:  a.EvidenceProvider,
		TracePayloadStore: a.TracePayloadStore,
		CheckpointStore:   a.CheckpointStore,
		ApprovalService:   a.ApprovalService,
		ToolAuthorizer: agent.NewToolAuthorizer(agentToolUserScopeResolver{
			members: tenantMemberService(c),
		}),
		Logger: c.Logger,
	}
	if db != nil {
		deps.ResourceEditorRepo = persistence.NewPgResourceEditorRepo(db)
	}
	if c.Memory != nil {
		deps.MemoryInjector = c.Memory.Injector
		deps.RecallMemory = c.Memory.RecallFn
	}
	if c.MCP != nil {
		deps.MCPTools = c.MCP.AgentToolProvider
		deps.MCPToolExecutor = agentMCPExecutor{clients: c.MCP.Manager}
		deps.MCPToolPolicy = agentMCPPolicyResolver{service: c.MCP.Service}
	}
	if c.Knowledge != nil && c.Knowledge.RAGService != nil {
		deps.RAGSearch = ragSearchAdapter{rag: c.Knowledge.RAGService}
	}
	if c.Platform != nil {
		deps.Metrics = c.Platform.Metrics
	}
	if c.Memory != nil && c.Memory.Service != nil {
		deps.MemoryCleaner = c.Memory.Service
		svc := c.Memory.Service
		deps.MemoryBuffer = func(ctx context.Context, tenantID, userID, agentID, conversationID, scope, role, content string) error {
			return svc.BufferMessage(ctx, &memapp.BufferMessageRequest{
				TenantID:       tenantID,
				UserID:         userID,
				AgentID:        agentID,
				ConversationID: conversationID,
				Scope:          scope,
				Role:           role,
				Content:        content,
				MessageID:      uuid.Must(uuid.NewV7()).String(),
				CreatedAt:      time.Now(),
			})
		}
	}
	a.DiagnosticProvider = newSystemAssistantDiagnosticAdapter(
		tenantRoleAdapter{service: tenantMemberService(c)}, systemAssistantDiagnosticCollectors(c, a),
	)
	deps.OfficialDocsSearch = officialdocs.Search
	wirePromptResolver(c, a)
	deps.DiagnosticProvider = a.DiagnosticProvider
	a.Service = agent.NewAgentService(deps)
	if db != nil && c.Skill != nil && c.MCP != nil && c.Knowledge != nil &&
		c.Skill.VersionService != nil && c.MCP.Service != nil && c.Knowledge.WorkspaceService != nil {
		c.injectTenantRoleResolvers(a)
		adapters := NewResourceChangeProposalAdapters(
			a.Service, c.Skill.VersionService, c.MCP.Service, c.Knowledge.WorkspaceService,
		)
		a.ProposalService = agent.NewResourceChangeProposalService(
			persistence.NewPgResourceChangeProposalRepo(db),
			proposalAuthorizer{roles: tenantRoleAdapter{service: tenantMemberService(c)}},
			adapters,
			map[domain.ResourceKind]agentport.ResourceChangeApplier{
				domain.ResourceAgent: adapters, domain.ResourceSkillDraft: adapters,
				domain.ResourceMCPConfig: adapters, domain.ResourceKnowledgeWorkspace: adapters,
			},
			deps.Metrics, // nil is normalized to NoopMetrics inside the constructor
		)
		a.Service.SetResourceChangeProposalService(a.ProposalService)
		a.Service.SetResourceChangeApplier(adapters.ApplyDirectFromTool)
	}
	wireOperationGate(c, a, deps.Metrics)
	c.Agent = a
	return nil
}

// injectTenantRoleResolvers hands one DB-backed role adapter to every service
// whose ownership checks need it. Called only when the full resource stack is
// wired; otherwise each service fails closed (nil resolver).
func (c *Container) injectTenantRoleResolvers(a *Agent) {
	roles := tenantRoleAdapter{service: tenantMemberService(c)}
	a.Service.SetTenantRoleResolver(roles)
	c.Skill.VersionService.SetTenantRoleResolver(roles)
	c.MCP.Service.SetTenantRoleResolver(roles)
	c.Knowledge.WorkspaceService.SetTenantRoleResolver(roles)
}

// wireOperationGate wires the T8 operation approval chain: the gate service
// plus the reviewer-facing proposal service, and injects the gate into the
// agent service. Skipped without a database.
func wireOperationGate(c *Container, a *Agent, metrics observability.MetricsProvider) {
	db := c.dbOrNil()
	if db == nil {
		return
	}
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	roles := tenantRoleAdapter{service: tenantMemberService(c)}
	a.OperationGateService = agent.NewOperationGateService(
		persistence.NewPgOperationProposalRepo(db),
		persistence.NewPgOperationUsageRepo(db),
		metrics,
	)
	a.OperationProposalSvc = agent.NewOperationProposalService(
		persistence.NewPgOperationProposalRepo(db),
		roles,
		metrics,
	)
	a.Service.SetOperationGate(a.OperationGateService)
}

func tenantModelValidator(resolver agentport.TenantCapabilityResolver) agentport.TenantChatModelValidator {
	validator, _ := resolver.(agentport.TenantChatModelValidator)
	return validator
}

func tenantModelCatalog(resolver agentport.TenantCapabilityResolver) agentport.TenantChatModelCatalog {
	catalog, _ := resolver.(agentport.TenantChatModelCatalog)
	return catalog
}

func modelContextProvider(resolver agentport.TenantCapabilityResolver) agentport.ModelContextProvider {
	provider, _ := resolver.(agentport.ModelContextProvider)
	return provider
}

// wirePromptResolver constructs the PromptResolver and injects the
// centralized prompt registry for versioned prompt resolution.
func wirePromptResolver(c *Container, a *Agent) {
	a.PromptResolver = agent.NewPromptResolver(nil)
	if c.Prompt != nil && c.Prompt.Registry != nil {
		a.PromptResolver.SetRegistry(c.Prompt.Registry)
	}
}
