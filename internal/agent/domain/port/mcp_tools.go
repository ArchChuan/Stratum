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
	ExecuteMCPTool(ctx context.Context, serverID, toolName string, input map[string]any) (MCPToolResult, error)
}

type MCPRevisionToolExecutor interface {
	ExecuteMCPToolRevision(
		ctx context.Context,
		serverID, toolName, revisionID string,
		risk ToolRiskLevel,
		input map[string]any,
	) (MCPToolResult, error)
}

type MCPContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Data any    `json:"data,omitempty"`
}

type MCPToolResult struct {
	Content           []MCPContent   `json:"content,omitempty"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

type GuardedToolResult struct {
	ModelContent      string
	Summary           string
	SHA256            string
	StructuredContent map[string]any
	Untrusted         bool
	Truncated         bool
	IsError           bool
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
	MCPRevisionID string
}

type ToolExecutionFn func(context.Context, ToolExecutionRequest) (any, error)
