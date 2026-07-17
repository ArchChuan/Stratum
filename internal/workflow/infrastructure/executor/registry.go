package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
)

type ToolRisk string

const (
	ToolRiskRead            ToolRisk = "read"
	ToolRiskWriteReversible ToolRisk = "write_reversible"
	ToolRiskDestructive     ToolRisk = "destructive"
	ToolRiskUnclassified    ToolRisk = "unclassified"
)

func (r ToolRisk) requiresApproval() bool {
	return r == ToolRiskDestructive || r == ToolRiskUnclassified
}

type AgentRuntime interface {
	ExecuteAgent(context.Context, string, string, string) (string, string, error)
}

type SkillRuntime interface {
	ExecuteSkill(context.Context, string, string, string, string, string) (string, string, error)
}

type MCPRuntime interface {
	ToolRisk(context.Context, string, string, string) (ToolRisk, error)
	CallTool(context.Context, string, string, string, map[string]any) (any, error)
}

type Registry struct {
	agent AgentRuntime
	skill SkillRuntime
	mcp   MCPRuntime
}

func NewRegistry(agent AgentRuntime, skill SkillRuntime, mcp MCPRuntime) *Registry {
	return &Registry{agent: agent, skill: skill, mcp: mcp}
}

func (r *Registry) Execute(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	switch request.Node.Type {
	case domain.NodeTypeAgent:
		if r.agent == nil {
			return port.NodeExecutionResult{}, fmt.Errorf("agent executor unavailable")
		}
		output, traceID, err := r.agent.ExecuteAgent(ctx, request.TenantID, request.Node.AgentID, request.Input)
		return port.NodeExecutionResult{Output: output, TraceID: traceID}, err
	case domain.NodeTypeSkill:
		if r.skill == nil {
			return port.NodeExecutionResult{}, fmt.Errorf("skill executor unavailable")
		}
		output, traceID, err := r.skill.ExecuteSkill(ctx, request.TenantID, request.Node.AgentID, request.Node.SkillID, request.Node.SkillRevisionID, request.Input)
		return port.NodeExecutionResult{Output: output, TraceID: traceID}, err
	case domain.NodeTypeMCPTool:
		return r.executeMCP(ctx, request)
	case domain.NodeTypeCondition:
		value, err := evaluateCondition(request.Node.Condition, request.RunInput, request.NodeOutputs)
		return port.NodeExecutionResult{ConditionValue: value}, err
	default:
		return port.NodeExecutionResult{}, fmt.Errorf("unsupported workflow node type %q", request.Node.Type)
	}
}

func (r *Registry) executeMCP(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	if r.mcp == nil {
		return port.NodeExecutionResult{}, fmt.Errorf("mcp executor unavailable")
	}
	risk, err := r.mcp.ToolRisk(ctx, request.TenantID, request.Node.MCPServerID, request.Node.MCPToolName)
	if err != nil {
		return port.NodeExecutionResult{}, err
	}
	if risk != ToolRiskRead && request.Node.EffectClass == domain.EffectClassPure {
		return port.NodeExecutionResult{}, fmt.Errorf("write-capable MCP tool cannot be classified pure")
	}
	if risk.requiresApproval() && (!request.Approved || request.ApprovalID == "") {
		return port.NodeExecutionResult{Paused: true, ErrorCode: "approval_required"}, nil
	}
	if request.BeforeEffect != nil {
		if err := request.BeforeEffect(); err != nil {
			return port.NodeExecutionResult{}, err
		}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(request.Input), &input); err != nil {
		input = map[string]any{"input": request.Input}
	}
	output, err := r.mcp.CallTool(ctx, request.TenantID, request.Node.MCPServerID, request.Node.MCPToolName, input)
	if err != nil {
		code := "mcp_provider_error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "timeout"
		}
		return port.NodeExecutionResult{Retryable: request.Node.EffectClass != domain.EffectClassNonIdempotent, ErrorCode: code}, err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return port.NodeExecutionResult{}, err
	}
	return port.NodeExecutionResult{Output: string(encoded)}, nil
}

func evaluateCondition(expression string, input map[string]any, outputs map[string]string) (bool, error) {
	parts := strings.Split(expression, "==")
	if len(parts) != 2 {
		return false, fmt.Errorf("unsupported condition expression")
	}
	left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	var actual any
	switch {
	case strings.HasPrefix(left, "input."):
		actual = input[strings.TrimPrefix(left, "input.")]
	case strings.HasPrefix(left, "nodes.") && strings.HasSuffix(left, ".output"):
		id := strings.TrimSuffix(strings.TrimPrefix(left, "nodes."), ".output")
		value, exists := outputs[id]
		if !exists {
			return false, fmt.Errorf("condition output for node %q is unavailable", id)
		}
		actual = value
	default:
		return false, fmt.Errorf("condition may only read input or completed node output")
	}
	expected, err := conditionLiteral(right)
	if err != nil {
		return false, err
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected), nil
}

func conditionLiteral(value string) (any, error) {
	if value == "true" || value == "false" {
		return strconv.ParseBool(value)
	}
	if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
		return value[1 : len(value)-1], nil
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		return number, nil
	}
	return nil, fmt.Errorf("unsupported condition literal")
}
