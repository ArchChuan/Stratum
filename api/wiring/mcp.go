package wiring

import (
	"context"

	"go.uber.org/zap"

	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
)

// MCP holds the Model-Context-Protocol client manager and the skill
// registry that exposes MCP tools to agents. The manager owns long-lived
// per-tenant client connections and is restored from DB on startup.
type MCP struct {
	Manager  *mcp.ClientManager
	Registry *mcp.MCPSkillRegistry
}

func (c *Container) buildMCP(ctx context.Context) error {
	var db = c.dbOrNil()
	manager := mcp.NewClientManager(c.Logger, nil, db)
	registry := mcp.NewMCPSkillRegistry(manager, c.Logger)

	if db != nil {
		if err := manager.RestoreFromDB(ctx); err != nil {
			c.Logger.Warn("failed to restore MCP servers from DB", zap.Error(err))
		}
	}

	c.MCP = &MCP{Manager: manager, Registry: registry}
	return nil
}
