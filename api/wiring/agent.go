package wiring

import (
	"context"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	persistence "github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
)

// Agent groups the agent persistence/registry services and execution
// stores. The Registry is wired with CapabilityGateway and MemoryInjector
// so agents resolved from DB inherit those capabilities at construction
// time. Service is the orchestration façade handlers consume.
type Agent struct {
	Registry       *agent.Registry
	Service        *agent.AgentService
	ExecStore      agent.ExecutionStore
	ChatStore      agent.ChatStore
	TenantResolver agentport.TenantCapabilityResolver
	SkillLookup    agentport.SkillLookup
	TenantSettings agentport.TenantSettings
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
	if c.Memory != nil && c.Memory.RecallFn != nil {
		registry.SetRecallMemoryFn(c.Memory.RecallFn)
	}

	a := &Agent{Registry: registry}
	if db != nil {
		a.ExecStore = persistence.NewPgExecutionStore(db)
		a.ChatStore = persistence.NewPgChatStore(db, c.Logger)
		a.SkillLookup = persistence.NewPgSkillLookup(db)
		a.TenantSettings = persistence.NewPgTenantSettings(db)
		if c.Skill != nil {
			a.TenantResolver = newTenantCapabilityResolver(db, c.Platform.AESKey, c.Platform.GatewayCache, c.Skill.SkillAdapter, c.Logger)
		}
	}

	deps := agent.AgentServiceDeps{
		Registry:       registry,
		TenantSettings: a.TenantSettings,
		SkillLookup:    a.SkillLookup,
		TenantResolver: a.TenantResolver,
		ExecStore:      a.ExecStore,
		ChatStore:      a.ChatStore,
		Logger:         c.Logger,
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
	a.Service = agent.NewAgentService(deps)

	c.Agent = a
	return nil
}
