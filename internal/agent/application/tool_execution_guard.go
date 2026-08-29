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
	RuleGuard       *RuleGuard // P1b：内联规则护栏，nil 时跳过（默认放行）
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
	if g.deps.RuleGuard != nil {
		if block, blocked := g.deps.RuleGuard.Check(ctx, req.Tool.Name); blocked {
			return nil, block
		}
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
	guarded, err := g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
	if err != nil {
		return nil, wrapGuardError(err)
	}
	return guarded, nil
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
	guarded, err := g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
	if err != nil {
		return nil, wrapGuardError(err)
	}
	return guarded, nil
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
	guarded, err := g.deps.ResultGuard.Validate(result, req.Tool.OutputSchema)
	if err != nil {
		return nil, wrapGuardError(err)
	}
	return guarded, nil
}

// wrapGuardError 把 ResultGuard 的校验错误翻译成带 Outcome 的执行错误，使 graph
// 层可通过 errors.As 解包到 definite_failure / outcome_unknown，供幻觉防护对账
// 使用。IsError=true 的结果是工具真实执行且明确报错 → definite_failure；schema
// 校验失败仅说明结果结构不达标，不等于成功执行 → outcome_unknown。
func wrapGuardError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrMCPToolResult) {
		return &port.MCPToolExecutionError{
			Outcome: port.ToolExecutionOutcomeDefiniteFailure, Err: err,
		}
	}
	if errors.Is(err, ErrMCPToolResultSchema) {
		return &port.MCPToolExecutionError{
			Outcome: port.ToolExecutionOutcomeUnknown, Err: err,
		}
	}
	return err
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
