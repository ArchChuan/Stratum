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
	risk := port.ParseToolRiskLevel(req.Tool.Metadata["risk_level"])
	policyResolved, _ := req.Tool.Metadata["policy_resolved"].(bool)
	decision := g.deps.Authorizer.Authorize(ctx, ToolAuthorizationInput{
		TenantID: req.TenantID, UserID: req.UserID, AgentID: req.AgentID, ToolID: toolID,
		AgentAllowsTool: agentAllows,
		PolicyResolved:  policyResolved, RiskLevel: risk,
	})
	if decision.Effect == domain.ToolAuthorizationDeny {
		return nil, fmt.Errorf("%w: %s", ErrToolAuthorizationDenied, decision.Reason)
	}
	if err := validateToolArguments(req.Tool.InputSchema, req.Arguments); err != nil {
		return nil, err
	}
	if req.ApprovalID != "" {
		return g.executeApproved(ctx, req)
	}
	if decision.Effect == domain.ToolAuthorizationRequireApproval {
		return g.requestApproval(ctx, req, decision)
	}
	if req.DelegateExecutor != nil {
		return g.executeDelegate(ctx, req)
	}
	return g.executeMCP(ctx, req, decision)
}

// executeApproved runs a tool call that carries a pre-issued approval ID,
// normalizing its result through the ResultGuard.
func (g *ToolExecutionGuard) executeApproved(ctx context.Context, req ToolExecutionRequest) (any, error) {
	if g.deps.ExecuteApproved == nil {
		return nil, fmt.Errorf("execute approved tool: runtime unavailable")
	}
	result, err := g.deps.ExecuteApproved(ctx, req)
	if err != nil {
		return nil, err
	}
	return g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
}

// requestApproval creates a pending approval and returns
// ToolApprovalRequiredError so the caller pauses for human decision.
func (g *ToolExecutionGuard) requestApproval(ctx context.Context, req ToolExecutionRequest, decision domain.ToolAuthorizationDecision) (any, error) {
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

// executeDelegate runs the stratum_delegate branch without touching the MCP
// executor: authorization and jsonschema validation already happened above, so
// the closure is invoked directly and its result still flows through the
// ResultGuard (redaction/truncation/<untrusted_tool_result> wrapping).
func (g *ToolExecutionGuard) executeDelegate(ctx context.Context, req ToolExecutionRequest) (any, error) {
	result, err := req.DelegateExecutor(ctx, req.Arguments)
	if err != nil {
		return nil, err
	}
	return g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
}

// executeMCP runs the regular MCP tool path (optionally a specific revision),
// translating the executor result through the ResultGuard.
func (g *ToolExecutionGuard) executeMCP(ctx context.Context, req ToolExecutionRequest, decision domain.ToolAuthorizationDecision) (any, error) {
	if g.deps.Executor == nil {
		return nil, fmt.Errorf("MCP tool executor not configured")
	}
	var result port.MCPToolResult
	var err error
	if req.MCPRevisionID != "" {
		revisionExecutor, ok := g.deps.Executor.(port.MCPRevisionToolExecutor)
		if !ok {
			return nil, fmt.Errorf("MCP revision executor not configured")
		}
		result, err = revisionExecutor.ExecuteMCPToolRevision(
			ctx, req.Tool.ServerID, req.Tool.CapabilityID, req.MCPRevisionID, decision.RiskLevel, req.Arguments,
		)
	} else {
		result, err = g.deps.Executor.ExecuteMCPTool(ctx, req.Tool.ServerID, req.Tool.CapabilityID, req.Arguments)
	}
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
