package wiring

import (
	"context"
	"fmt"
	"time"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	agentobjects "github.com/byteBuilderX/stratum/internal/agent/infrastructure/objectstore"
	agentopik "github.com/byteBuilderX/stratum/internal/agent/infrastructure/opik"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	memapp "github.com/byteBuilderX/stratum/internal/memory/application"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"
)

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time. Service is the orchestration façade handlers consume.
type Agent struct {
	Registry            *agent.Registry
	Service             *agent.AgentService
	ChatStore           agent.ChatStore
	EvidenceProvider    agentport.TraceEvidenceProvider
	TracePayloadStore   agentport.TracePayloadStore
	RevisionObjectStore pkgobjectstore.Store
	CheckpointStore     agent.CheckpointStore
	ApprovalStore       agentport.ToolApprovalRepo
	ApprovalService     *agent.ToolApprovalService
	TenantResolver      agentport.TenantCapabilityResolver
	SkillLookup         agentport.SkillLookup
	TenantSettings      agentport.TenantSettings
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
			MCPToolIDs:            view.MCPToolIDs,
			KnowledgeWorkspaceIDs: view.KnowledgeWorkspaceIDs,
			MemoryScopes:          view.MemoryScopes,
		}
	}
	return catalog, nil
}

func (c *Container) buildAgent(ctx context.Context) error {
	db := c.dbOrNil()

	var registry *agent.Registry
	if db != nil {
		registry = agent.NewRegistry(persistence.NewPgAgentRepo(db), c.Logger)
	} else {
		registry = agent.NewRegistry(nil, c.Logger)
	}
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
		client, err := minio.New(c.Config.TracePayload.Endpoint, &minio.Options{
			Creds: credentials.NewStaticV4(
				c.Config.TracePayload.AccessKey, c.Config.TracePayload.SecretKey, "",
			),
			Secure: c.Config.TracePayload.UseTLS,
		})
		if err != nil {
			c.Logger.Warn("trace payload client initialization failed", zap.Error(err))
		} else {
			store := agentobjects.NewStore(
				client, c.Config.TracePayload.Bucket, c.Platform.AESKey,
			)
			bucketCtx, cancel := context.WithTimeout(ctx, c.Config.Opik.Timeout)
			if err := store.EnsureBucket(bucketCtx); err != nil {
				c.Logger.Warn("trace payload bucket unavailable", zap.Error(err))
			}
			cancel()
			a.TracePayloadStore = store
			a.RevisionObjectStore = store.GenericStore()
		}
	}
	if db != nil {
		a.ChatStore = persistence.NewPgChatStore(db, c.Logger)
		a.CheckpointStore = persistence.NewPgCheckpointStore(db)
		a.ApprovalStore = persistence.NewPgToolApprovalStore(db)
		a.ApprovalService = agent.NewToolApprovalService(a.ApprovalStore, a.CheckpointStore, c.Platform.AESKey)
		a.SkillLookup = persistence.NewPgSkillLookup(db)
		a.TenantSettings = persistence.NewPgTenantSettings(db)
		var fallbackGateway *llmgateway.Gateway
		if c.LLMGateway != nil {
			fallbackGateway = c.LLMGateway.Gateway
		}
		a.TenantResolver = newTenantCapabilityResolver(
			db, c.Platform.AESKey, c.Platform.GatewayCache, fallbackGateway, c.Logger,
			c.Config.QwenBaseURL, c.Config.ZhipuBaseURL,
		)
	}

	deps := agent.AgentServiceDeps{
		Registry:                registry,
		TenantSettings:          a.TenantSettings,
		SkillLookup:             a.SkillLookup,
		SkillActivationResolver: publishedSkillActivationResolver{versions: skillVersionService(c)},
		TenantResolver:          a.TenantResolver,
		HistoryCompactorFactory: func(gw agentport.CapabilityGateway, model string, logger *zap.Logger) agentport.HistoryCompactor {
			return capgateway.NewLLMHistoryCompactor(gw, model, logger)
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
	a.Service = agent.NewAgentService(deps)

	c.Agent = a
	return nil
}
