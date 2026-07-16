package wiring

import (
	"context"
	"encoding/json"
	"time"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	memapp "github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time. Service is the orchestration façade handlers consume.
type Agent struct {
	Registry        *agent.Registry
	Service         *agent.AgentService
	ExecStore       agent.ExecutionStore
	ChatStore       agent.ChatStore
	ToolTraceStore  agent.ToolTraceStore
	TraceEventStore agent.TraceEventStore
	CheckpointStore agent.CheckpointStore
	ApprovalStore   agentport.ToolApprovalRepo
	ApprovalService *agent.ToolApprovalService
	TenantResolver  agentport.TenantCapabilityResolver
	SkillLookup     agentport.SkillLookup
	TenantSettings  agentport.TenantSettings
}

// ragSearchAdapter wraps *knowledge.RAGService to satisfy
// agentport.RAGSearchProvider. Lives in wiring (the composition root)
// so neither agent/application nor knowledge/application has to know
// about the other.
type ragSearchAdapter struct {
	rag *knowledge.RAGService
}

func (a ragSearchAdapter) SearchKnowledge(
	ctx context.Context, tenantID string, workspaceIDs []string, query string, topK int,
) (string, error) {
	return knowledge.NewRAGSearchFn(a.rag, tenantID)(ctx, workspaceIDs, query, topK)
}

type publishedSkillActivationResolver struct {
	db *pgxpool.Pool
}

func (r publishedSkillActivationResolver) ResolveSkills(
	ctx context.Context, _ string, refs []agentport.SkillRevisionRef,
) (map[string]agentport.SkillActivation, error) {
	catalog := make(map[string]agentport.SkillActivation, len(refs))
	if r.db == nil || len(refs) == 0 {
		return catalog, nil
	}
	err := tenantdb.ExecTenant(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		for _, ref := range refs {
			var skillID, revisionID, skillName, skillDescription, instructions string
			var activationRaw, requirementsRaw []byte
			err := tx.QueryRow(ctx,
				`SELECT s.id, r.id, s.name, s.description, r.instructions,
				        r.activation_contract, r.requirements
				 FROM skills s JOIN skill_revisions r
				   ON r.id=COALESCE(NULLIF($2, ''), s.active_revision_id)
				 WHERE s.id=$1 AND r.status IN ('published', 'candidate')`,
				ref.SkillID, ref.RevisionID,
			).Scan(&skillID, &revisionID, &skillName, &skillDescription, &instructions, &activationRaw, &requirementsRaw)
			if err == pgx.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			var activation struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			var requirements struct {
				MCPToolIDs            []string `json:"mcpToolIds"`
				KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
				MemoryScopes          []string `json:"memoryScopes"`
			}
			if err := json.Unmarshal(activationRaw, &activation); err != nil {
				return err
			}
			if err := json.Unmarshal(requirementsRaw, &requirements); err != nil {
				return err
			}
			if activation.Name == "" {
				activation.Name = skillName
			}
			if activation.Description == "" {
				activation.Description = skillDescription
			}
			catalog[skillID] = agentport.SkillActivation{
				SkillID: skillID, RevisionID: revisionID, Name: activation.Name,
				Description: activation.Description, Instructions: instructions,
				MCPToolIDs: requirements.MCPToolIDs, KnowledgeWorkspaceIDs: requirements.KnowledgeWorkspaceIDs,
				MemoryScopes: requirements.MemoryScopes,
			}
		}
		return nil
	})
	return catalog, err
}

func (c *Container) buildAgent(_ context.Context) error {
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

	a := &Agent{Registry: registry}
	if db != nil {
		a.ExecStore = persistence.NewPgExecutionStore(db)
		a.ChatStore = persistence.NewPgChatStore(db, c.Logger)
		a.ToolTraceStore = persistence.NewPgToolTraceStore(db)
		a.TraceEventStore = persistence.NewPgTraceEventStore(db)
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
		SkillActivationResolver: publishedSkillActivationResolver{db: db},
		TenantResolver:          a.TenantResolver,
		ExecStore:               a.ExecStore,
		ChatStore:               a.ChatStore,
		ToolTraceStore:          a.ToolTraceStore,
		TraceEventStore:         a.TraceEventStore,
		CheckpointStore:         a.CheckpointStore,
		ApprovalService:         a.ApprovalService,
		Logger:                  c.Logger,
	}
	if c.MCP != nil {
		deps.MCPTools = c.MCP.AgentToolProvider
		deps.MCPToolExecutor = agentMCPExecutor{manager: c.MCP.Manager}
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
