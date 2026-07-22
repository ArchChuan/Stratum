package port

import (
	"context"
	"fmt"
)

// MCPToolProvider is the consumer-side port for retrieving MCP tool definitions.
// The handler uses this to build extra tools for the ReAct loop without importing
// MCP infrastructure directly.
type MCPToolProvider interface {
	ToolsForServer(ctx context.Context, serverID string) []ToolDefinition
}

type MCPToolExecutor interface {
	ExecuteMCPTool(ctx context.Context, serverID, toolName string, input map[string]any) (any, error)
}

type ToolExecutionOutcome string

const (
	ToolExecutionOutcomeNotSent         ToolExecutionOutcome = "not_sent"
	ToolExecutionOutcomeDefiniteFailure ToolExecutionOutcome = "definite_failure"
	ToolExecutionOutcomeUnknown         ToolExecutionOutcome = "outcome_unknown"
)

type MCPToolExecutionError struct {
	Outcome ToolExecutionOutcome
	Err     error
}

func (e *MCPToolExecutionError) Error() string {
	return fmt.Sprintf("MCP tool execution %s: %v", e.Outcome, e.Err)
}

func (e *MCPToolExecutionError) Unwrap() error { return e.Err }

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
