package infrastructure

import (
	"context"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpport "github.com/byteBuilderX/stratum/internal/mcp/domain/port"
)

// SkillRegistryAsPort wraps MCPSkillRegistry to satisfy port.SkillRegistry.
func SkillRegistryAsPort(r *MCPSkillRegistry) mcpport.SkillRegistry {
	return &skillRegistryPortAdapter{r: r}
}

type skillRegistryPortAdapter struct {
	r *MCPSkillRegistry
}

func (a *skillRegistryPortAdapter) RegisterServer(ctx context.Context, serverID string) error {
	return a.r.RegisterServer(ctx, serverID)
}
func (a *skillRegistryPortAdapter) ExecuteSkill(skillID string, input any) (any, error) {
	return a.r.ExecuteSkill(skillID, input)
}
func (a *skillRegistryPortAdapter) GetSkill(id string) mcpport.SkillAccessor {
	w := a.r.GetSkill(id)
	if w == nil {
		return nil
	}
	return w
}
func (a *skillRegistryPortAdapter) GetAllSkills() []mcpport.SkillAccessor {
	raw := a.r.GetAllSkills()
	out := make([]mcpport.SkillAccessor, len(raw))
	for i, w := range raw {
		out[i] = w
	}
	return out
}
func (a *skillRegistryPortAdapter) RefreshSkills(ctx context.Context) error {
	return a.r.RefreshSkills(ctx)
}

// ServerManagerAsPort wraps ClientManager to satisfy port.ServerManager.
func ServerManagerAsPort(m *ClientManager) mcpport.ServerManager {
	return m
}

// RegistryAsAgentToolProvider wraps MCPSkillRegistry to satisfy agentport.MCPToolProvider.
func RegistryAsAgentToolProvider(r *MCPSkillRegistry) agentport.MCPToolProvider {
	return &mcpAgentToolAdapter{r: r}
}

type mcpAgentToolAdapter struct {
	r *MCPSkillRegistry
}

func (a *mcpAgentToolAdapter) ToolsForServer(ctx context.Context, serverID string) []agentport.ToolDefinition {
	adapter := a.r.GetAdapterForServer(serverID)
	if adapter == nil {
		return nil
	}
	skills := adapter.GetAllSkills()
	tools := make([]agentport.ToolDefinition, 0, len(skills))
	for _, w := range skills {
		tools = append(tools, agentport.ToolDefinition{
			Name:         w.GetID(),
			Description:  w.Tool.Description,
			InputSchema:  w.Tool.InputSchema,
			ProviderType: "mcp",
			ProviderID:   serverID,
			ServerID:     serverID,
			CapabilityID: w.GetID(),
			NodeType:     "mcp",
		})
	}
	return tools
}
