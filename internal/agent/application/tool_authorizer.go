package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

type ToolAuthorizationInput struct {
	TenantID        string
	UserID          string
	AgentID         string
	ToolID          string
	AgentAllowsTool bool
	PolicyResolved  bool
	RiskLevel       domain.ToolRiskLevel
}

type ToolAuthorizer struct {
	userScopes port.ToolUserScopeResolver
}

func NewToolAuthorizer(userScopes port.ToolUserScopeResolver) *ToolAuthorizer {
	return &ToolAuthorizer{userScopes: userScopes}
}

func (a *ToolAuthorizer) Authorize(ctx context.Context, input ToolAuthorizationInput) domain.ToolAuthorizationDecision {
	if input.TenantID == "" {
		return deniedToolAuthorization(input.RiskLevel, domain.ToolReasonTenantContextMissing)
	}
	if input.UserID == "" {
		return deniedToolAuthorization(input.RiskLevel, domain.ToolReasonUserInactive)
	}
	if a == nil || a.userScopes == nil {
		return deniedToolAuthorization(input.RiskLevel, domain.ToolReasonPolicyLookupFailed)
	}

	scope, err := a.userScopes.ResolveToolUserScope(
		ctx, input.TenantID, input.UserID, input.AgentID, input.ToolID,
	)
	if err != nil {
		return deniedToolAuthorization(input.RiskLevel, domain.ToolReasonPolicyLookupFailed)
	}

	req := domain.ToolAuthorizationRequest{
		TenantID:        input.TenantID,
		UserID:          input.UserID,
		ToolID:          input.ToolID,
		UserActive:      scope.UserActive,
		UserAllowsTool:  scope.AllowsTool,
		AgentAllowsTool: input.AgentAllowsTool,
		PolicyResolved:  input.PolicyResolved,
		RiskLevel:       input.RiskLevel,
	}
	// 所有 agent 一视同仁：不再按 AgentID 区分平台助手，统一走宽松授权
	// 模型。未配置 policy（policy_resolved=false）的工具默认 require_approval
	// （含 read）；管理员经 SetToolPolicy 配置 risk_level 后放行。
	return domain.AuthorizeTool(req)
}

func deniedToolAuthorization(
	risk domain.ToolRiskLevel,
	reason domain.ToolAuthorizationReason,
) domain.ToolAuthorizationDecision {
	return domain.ToolAuthorizationDecision{
		Effect: domain.ToolAuthorizationDeny, Reason: reason, RiskLevel: risk,
	}
}
