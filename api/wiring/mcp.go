package wiring

import (
	"context"
	"time"

	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
)

// MCP holds the Model-Context-Protocol client manager and tool
// registry exposed to agents. The manager owns long-lived
// per-tenant client connections and is restored from DB on startup.
type MCP struct {
	Manager           *mcp.ClientManager
	Registry          *mcp.MCPToolRegistry
	Service           *mcpapp.MCPService
	AgentToolProvider agentport.MCPToolProvider
}

type agentMCPExecutor struct{ manager *mcp.ClientManager }

func (e agentMCPExecutor) ExecuteMCPTool(ctx context.Context, serverID, toolName string, input map[string]any) (any, error) {
	return e.manager.CallTool(ctx, serverID, toolName, input)
}

type agentMCPPolicyResolver struct{ service *mcpapp.MCPService }

func (r agentMCPPolicyResolver) ResolveMCPToolRisk(ctx context.Context, _, serverID, toolName string) (agentport.ToolRiskLevel, error) {
	level, err := r.service.GetToolRisk(ctx, serverID, toolName)
	return agentport.ToolRiskLevel(level), err
}

func (c *Container) buildMCP(ctx context.Context) error {
	var db = c.dbOrNil()
	manager := mcp.NewClientManager(c.Logger, nil, db)
	registry := mcp.NewMCPToolRegistry(manager, c.Logger)
	svc := mcpapp.NewMCPService(
		mcp.ToolRegistryAsPort(registry),
		mcp.ServerManagerAsPort(manager),
		c.Logger,
	)
	if db != nil {
		svc.SetToolPolicyRepo(mcp.NewPgToolPolicyRepo(db))
	}

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
