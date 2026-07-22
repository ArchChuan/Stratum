package port

import "context"

// MCPToolProvider is the consumer-side port for retrieving MCP tool definitions.
// The handler uses this to build extra tools for the ReAct loop without importing
// MCP infrastructure directly.
type MCPToolProvider interface {
	ToolsForServer(ctx context.Context, serverID string) []ToolDefinition
}

type MCPToolExecutor interface {
	ExecuteMCPTool(ctx context.Context, serverID, toolName string, input map[string]any) (any, error)
}

type ToolExecutionRequest struct {
	TenantID      string
	UserID        string
	AgentID       string
	TraceID       string
	ExecutionID   string
	ToolCallID    string
	Tool          ToolDefinition
	Arguments     map[string]any
	AgentToolIDs  []string
	ActiveSkill   *SkillActivation
	ApprovalID    string
	PolicyVersion string
}

type ToolExecutionFn func(context.Context, ToolExecutionRequest) (any, error)
