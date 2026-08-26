package domain

type ToolRiskLevel string

const (
	ToolRiskRead            ToolRiskLevel = "read"
	ToolRiskWriteReversible ToolRiskLevel = "write_reversible"
	ToolRiskDestructive     ToolRiskLevel = "destructive"
	ToolRiskUnclassified    ToolRiskLevel = "unclassified"
)

func (r ToolRiskLevel) RequiresApproval() bool {
	return r == ToolRiskDestructive || r == ToolRiskUnclassified
}

type ToolAuthorizationEffect string

const (
	ToolAuthorizationDeny            ToolAuthorizationEffect = "deny"
	ToolAuthorizationAllow           ToolAuthorizationEffect = "allow"
	ToolAuthorizationRequireApproval ToolAuthorizationEffect = "require_approval"
)

type ToolAuthorizationReason string

const (
	ToolReasonTenantContextMissing ToolAuthorizationReason = "tenant_context_missing"
	ToolReasonUserInactive         ToolAuthorizationReason = "user_inactive"
	ToolReasonUserPermissionDenied ToolAuthorizationReason = "user_permission_denied"
	ToolReasonToolNotAllowlisted   ToolAuthorizationReason = "tool_not_allowlisted"
	ToolReasonPolicyLookupFailed   ToolAuthorizationReason = "policy_lookup_failed"
	ToolReasonToolUnclassified     ToolAuthorizationReason = "tool_unclassified"
	ToolReasonRiskAllowed          ToolAuthorizationReason = "risk_allowed"
)

type ToolAuthorizationRequest struct {
	TenantID        string
	UserID          string
	ToolID          string
	UserActive      bool
	UserAllowsTool  bool
	AgentAllowsTool bool
	PolicyResolved  bool
	RiskLevel       ToolRiskLevel
}

type ToolAuthorizationDecision struct {
	Effect    ToolAuthorizationEffect
	Reason    ToolAuthorizationReason
	RiskLevel ToolRiskLevel
}

func AuthorizeTool(req ToolAuthorizationRequest) ToolAuthorizationDecision {
	decision := ToolAuthorizationDecision{Effect: ToolAuthorizationDeny, RiskLevel: req.RiskLevel}
	switch {
	case req.TenantID == "":
		decision.Reason = ToolReasonTenantContextMissing
	case !req.UserActive:
		decision.Reason = ToolReasonUserInactive
	case !req.UserAllowsTool:
		decision.Reason = ToolReasonUserPermissionDenied
	case !req.AgentAllowsTool:
		decision.Reason = ToolReasonToolNotAllowlisted
	case !req.PolicyResolved:
		decision.Effect = ToolAuthorizationRequireApproval
		decision.Reason = ToolReasonPolicyLookupFailed
		decision.RiskLevel = ToolRiskUnclassified
	case req.RiskLevel == ToolRiskRead || req.RiskLevel == ToolRiskWriteReversible:
		decision.Effect = ToolAuthorizationAllow
		decision.Reason = ToolReasonRiskAllowed
	case req.RiskLevel == ToolRiskDestructive:
		// 管理员显式配置（policy_resolved=true）的 destructive 工具直接放行，
		// 配置即授权；未配置的工具已被上方 !PolicyResolved 分支拦截为
		// require_approval。用户裁决：destructive 配置后不再审批。
		decision.Effect = ToolAuthorizationAllow
		decision.Reason = ToolReasonRiskAllowed
	default:
		decision.Effect = ToolAuthorizationRequireApproval
		decision.Reason = ToolReasonToolUnclassified
		decision.RiskLevel = ToolRiskUnclassified
	}
	return decision
}
