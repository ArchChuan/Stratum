package workflow

import "github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"

// ReActRequest is the workflow input.
type ReActRequest struct {
	TraceID        string
	TenantID       string
	AgentID        string
	Input          string
	AgentCfg       AgentWorkflowConfig
	AvailableTools []capgateway.ToolDefinition
}

// AgentWorkflowConfig holds the fields from AgentConfig that the workflow needs.
type AgentWorkflowConfig struct {
	ID            string
	Name          string
	LLMModel      string
	SystemPrompt  string
	MaxIterations int
}

// ReActResult is the workflow output.
type ReActResult struct {
	Output    string
	ToolCalls []capgateway.ToolCall
	Steps     int
}
