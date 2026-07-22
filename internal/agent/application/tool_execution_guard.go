package application

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

var (
	ErrToolAuthorizationDenied = errors.New("tool execution authorization denied")
	ErrToolArgumentsInvalid    = errors.New("tool execution arguments invalid")
)

type ToolExecutionRequest = port.ToolExecutionRequest

type ApprovedToolExecutor func(context.Context, ToolExecutionRequest) (port.MCPToolResult, error)

type ToolExecutionGuardDeps struct {
	Authorizer      *ToolAuthorizer
	Executor        port.MCPToolExecutor
	RequestApproval port.ToolApprovalRequester
	ExecuteApproved ApprovedToolExecutor
	ResultGuard     *ToolResultGuard
}

type ToolExecutionGuard struct {
	deps ToolExecutionGuardDeps
}

func NewToolExecutionGuard(deps ToolExecutionGuardDeps) *ToolExecutionGuard {
	if deps.ResultGuard == nil {
		deps.ResultGuard = NewToolResultGuard()
	}
	return &ToolExecutionGuard{deps: deps}
}

func (g *ToolExecutionGuard) Execute(ctx context.Context, req ToolExecutionRequest) (any, error) {
	if g == nil || g.deps.Authorizer == nil {
		return nil, fmt.Errorf("%w: %s", ErrToolAuthorizationDenied, domain.ToolReasonPolicyLookupFailed)
	}
	toolID := req.Tool.Name
	agentAllows := slices.Contains(req.AgentToolIDs, toolID)
	activeSkill := req.ActiveSkill != nil
	activeSkillAllows := !activeSkill || slices.Contains(req.ActiveSkill.MCPToolIDs, toolID)
	risk := port.ParseToolRiskLevel(req.Tool.Metadata["risk_level"])
	policyResolved, _ := req.Tool.Metadata["policy_resolved"].(bool)
	decision := g.deps.Authorizer.Authorize(ctx, ToolAuthorizationInput{
		TenantID: req.TenantID, UserID: req.UserID, AgentID: req.AgentID, ToolID: toolID,
		AgentAllowsTool: agentAllows, ActiveSkill: activeSkill, ActiveSkillAllows: activeSkillAllows,
		PolicyResolved: policyResolved, RiskLevel: risk,
	})
	if decision.Effect == domain.ToolAuthorizationDeny {
		return nil, fmt.Errorf("%w: %s", ErrToolAuthorizationDenied, decision.Reason)
	}
	if err := validateToolArguments(req.Tool.InputSchema, req.Arguments); err != nil {
		return nil, err
	}
	if req.ApprovalID != "" {
		if g.deps.ExecuteApproved == nil {
			return nil, fmt.Errorf("execute approved tool: runtime unavailable")
		}
		result, err := g.deps.ExecuteApproved(ctx, req)
		if err != nil {
			return nil, err
		}
		return g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
	}
	if decision.Effect == domain.ToolAuthorizationRequireApproval {
		approvalID := ""
		if g.deps.RequestApproval != nil {
			var err error
			approvalID, err = g.deps.RequestApproval(ctx, port.ToolApprovalRequest{
				TenantID: req.TenantID, TraceID: req.TraceID, ExecutionID: req.ExecutionID,
				ToolCallID: req.ToolCallID, ServerID: req.Tool.ServerID,
				ToolName: req.Tool.CapabilityID, RiskLevel: decision.RiskLevel, Arguments: req.Arguments,
			})
			if err != nil {
				return nil, fmt.Errorf("create tool approval: %w", err)
			}
		}
		return nil, &port.ToolApprovalRequiredError{
			ApprovalID: approvalID, ToolCallID: req.ToolCallID, ServerID: req.Tool.ServerID,
			ToolName: req.Tool.CapabilityID, RiskLevel: decision.RiskLevel,
		}
	}
	if g.deps.Executor == nil {
		return nil, fmt.Errorf("MCP tool executor not configured")
	}
	result, err := g.deps.Executor.ExecuteMCPTool(ctx, req.Tool.ServerID, req.Tool.CapabilityID, req.Arguments)
	if err != nil {
		return nil, err
	}
	return g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
}

func validateToolArguments(schema, arguments map[string]any) error {
	if len(schema) == 0 {
		return fmt.Errorf("%w: input schema missing", ErrToolArgumentsInvalid)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const schemaURL = "urn:stratum:agent-tool-input"
	if err := compiler.AddResource(schemaURL, schema); err != nil {
		return fmt.Errorf("%w: compile resource: %v", ErrToolArgumentsInvalid, err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("%w: compile schema: %v", ErrToolArgumentsInvalid, err)
	}
	if err := compiled.Validate(arguments); err != nil {
		return fmt.Errorf("%w: %v", ErrToolArgumentsInvalid, err)
	}
	return nil
}
