package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpport "github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
)

var _ mcpport.RevisionClientManager = (*mcp.ClientManager)(nil)

// MCP holds the Model-Context-Protocol client manager and tool
// registry exposed to agents. The manager owns long-lived
// per-tenant client connections and is restored from DB on startup.
type MCP struct {
	Manager           *mcp.ClientManager
	Registry          *mcp.MCPToolRegistry
	Service           *mcpapp.MCPService
	AgentToolProvider agentport.MCPToolProvider
}

type mcpClientResolver interface {
	GetClient(ctx context.Context, serverID string) mcp.MCPClient
}

type agentMCPExecutor struct{ clients mcpClientResolver }

func (e agentMCPExecutor) ExecuteMCPTool(
	ctx context.Context, serverID, toolName string, input map[string]any,
) (agentport.MCPToolResult, error) {
	if e.clients == nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP client resolver unavailable"),
		}
	}
	client := e.clients.GetClient(ctx, serverID)
	if client == nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP client not found: %s", serverID),
		}
	}
	output, err := client.CallTool(ctx, toolName, input)
	if err != nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeUnknown,
			Err:     err,
		}
	}
	return normalizeMCPToolResult(output)
}

func normalizeMCPToolResult(output any) (agentport.MCPToolResult, error) {
	if result, ok := output.(agentport.MCPToolResult); ok {
		return result, nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("decode MCP tool result: unsupported result")
	}
	var result agentport.MCPToolResult
	if err := json.Unmarshal(raw, &result); err == nil &&
		(len(result.Content) > 0 || result.StructuredContent != nil || result.IsError) {
		return result, nil
	}
	if object, ok := output.(map[string]any); ok {
		return agentport.MCPToolResult{StructuredContent: object}, nil
	}
	return agentport.MCPToolResult{
		Content: []agentport.MCPContent{{Type: "text", Text: fmt.Sprint(output)}},
	}, nil
}

type agentMCPPolicyResolver struct{ service *mcpapp.MCPService }

type mcpAgentToolAdapter struct{ registry *mcp.MCPToolRegistry }

func (a mcpAgentToolAdapter) ToolsForServer(_ context.Context, serverID string) []agentport.ToolDefinition {
	catalog := a.registry.GetCatalogForServer(serverID)
	if catalog == nil {
		return nil
	}
	handles := catalog.GetAllTools()
	tools := make([]agentport.ToolDefinition, 0, len(handles))
	for _, handle := range handles {
		tools = append(tools, agentport.ToolDefinition{
			Name:         handle.GetID(),
			Description:  handle.Tool.Description,
			InputSchema:  handle.Tool.InputSchema,
			OutputSchema: handle.Tool.OutputSchema,
			ProviderType: "mcp",
			ProviderID:   serverID,
			ServerID:     serverID,
			CapabilityID: handle.Tool.Name,
			NodeType:     "mcp",
		})
	}
	return tools
}

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
		AgentToolProvider: mcpAgentToolAdapter{registry: registry},
	}
	return nil
}
