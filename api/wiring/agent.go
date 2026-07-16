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

type publishedSkillToolResolver struct {
	db *pgxpool.Pool
}

func (r publishedSkillToolResolver) ResolveTools(
	ctx context.Context,
	_ string,
	skillIDs []string,
) ([]agentport.ToolDefinition, map[string]agentport.SkillToolRef, error) {
	tools := make([]agentport.ToolDefinition, 0, len(skillIDs))
	index := make(map[string]agentport.SkillToolRef, len(skillIDs))
	if r.db == nil || len(skillIDs) == 0 {
		return tools, index, nil
	}
	err := tenantdb.ExecTenant(ctx, r.db, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT s.id, v.id, v.tool_contract
			 FROM skills s
			 JOIN skill_versions v ON v.id = s.active_version_id
			 WHERE s.id = ANY($1) AND v.status = 'published'`,
			skillIDs,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var skillID, versionID string
			var raw []byte
			if err := rows.Scan(&skillID, &versionID, &raw); err != nil {
				return err
			}
			var contract struct {
				ToolName        string         `json:"toolName"`
				Description     string         `json:"description"`
				InputSchema     map[string]any `json:"inputSchema"`
				CallingGuidance string         `json:"callingGuidance"`
			}
			if err := json.Unmarshal(raw, &contract); err != nil {
				return err
			}
			if contract.ToolName == "" {
				continue
			}
			description := contract.Description
			if contract.CallingGuidance != "" {
				description = description + "\n调用指引：" + contract.CallingGuidance
			}
			if contract.InputSchema == nil {
				contract.InputSchema = map[string]any{"type": "object"}
			}
			tools = append(tools, agentport.ToolDefinition{
				Name:        contract.ToolName,
				Description: description,
				InputSchema: contract.InputSchema,
			})
			index[contract.ToolName] = agentport.SkillToolRef{SkillID: skillID, VersionID: versionID}
		}
		return rows.Err()
	})
	return tools, index, err
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
		a.SkillLookup = persistence.NewPgSkillLookup(db)
		a.TenantSettings = persistence.NewPgTenantSettings(db)
		var skillAdapter agentport.Adapter
		if c.Skill != nil {
			skillAdapter = c.Skill.SkillAdapter
		}
		var fallbackGateway *llmgateway.Gateway
		if c.LLMGateway != nil {
			fallbackGateway = c.LLMGateway.Gateway
		}
		a.TenantResolver = newTenantCapabilityResolver(db, c.Platform.AESKey, c.Platform.GatewayCache, fallbackGateway, skillAdapter, c.Logger)
	}

	deps := agent.AgentServiceDeps{
		Registry:          registry,
		TenantSettings:    a.TenantSettings,
		SkillLookup:       a.SkillLookup,
		SkillToolResolver: publishedSkillToolResolver{db: db},
		TenantResolver:    a.TenantResolver,
		ExecStore:         a.ExecStore,
		ChatStore:         a.ChatStore,
		ToolTraceStore:    a.ToolTraceStore,
		TraceEventStore:   a.TraceEventStore,
		CheckpointStore:   a.CheckpointStore,
		Logger:            c.Logger,
	}
	if c.MCP != nil {
		deps.MCPTools = c.MCP.AgentToolProvider
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
