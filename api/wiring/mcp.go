package wiring

import (
	"context"
	"time"

	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
)

// MCP holds the Model-Context-Protocol client manager and the skill
// registry that exposes MCP tools to agents. The manager owns long-lived
// per-tenant client connections and is restored from DB on startup.
type MCP struct {
	Manager           *mcp.ClientManager
	Registry          *mcp.MCPSkillRegistry
	Service           *mcpapp.MCPService
	AgentToolProvider agentport.MCPToolProvider
}

func (c *Container) buildMCP(ctx context.Context) error {
	var db = c.dbOrNil()
	manager := mcp.NewClientManager(c.Logger, nil, db)
	registry := mcp.NewMCPSkillRegistry(manager, c.Logger)
	svc := mcpapp.NewMCPService(
		mcp.SkillRegistryAsPort(registry),
		mcp.ServerManagerAsPort(manager),
		c.Logger,
	)

	if db != nil {
		if err := manager.RestoreFromDB(ctx); err != nil {
			c.Logger.Warn("failed to restore MCP servers from DB", zap.Error(err))
		}
	}

	manager.StartHealthCheck(30 * time.Second)
	c.shutdown = append(c.shutdown, manager.Stop)

	c.MCP = &MCP{
		Manager:           manager,
		Registry:          registry,
		Service:           svc,
		AgentToolProvider: mcp.RegistryAsAgentToolProvider(registry),
	}
	return nil
}
