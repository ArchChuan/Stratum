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
	ToolReasonSkillScopeExceeded   ToolAuthorizationReason = "skill_scope_exceeded"
	ToolReasonPolicyLookupFailed   ToolAuthorizationReason = "policy_lookup_failed"
	ToolReasonToolUnclassified     ToolAuthorizationReason = "tool_unclassified"
	ToolReasonApprovalRequired     ToolAuthorizationReason = "approval_required"
	ToolReasonRiskAllowed          ToolAuthorizationReason = "risk_allowed"
)

type ToolAuthorizationRequest struct {
	TenantID          string
	UserID            string
	ToolID            string
	UserActive        bool
	UserAllowsTool    bool
	AgentAllowsTool   bool
	ActiveSkill       bool
	ActiveSkillAllows bool
	PolicyResolved    bool
	RiskLevel         ToolRiskLevel
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
	case req.ActiveSkill && !req.ActiveSkillAllows:
		decision.Reason = ToolReasonSkillScopeExceeded
	case !req.PolicyResolved:
		decision.Effect = ToolAuthorizationRequireApproval
		decision.Reason = ToolReasonPolicyLookupFailed
		decision.RiskLevel = ToolRiskUnclassified
	case req.RiskLevel == ToolRiskRead || req.RiskLevel == ToolRiskWriteReversible:
		decision.Effect = ToolAuthorizationAllow
		decision.Reason = ToolReasonRiskAllowed
	case req.RiskLevel == ToolRiskDestructive:
		decision.Effect = ToolAuthorizationRequireApproval
		decision.Reason = ToolReasonApprovalRequired
	default:
		decision.Effect = ToolAuthorizationRequireApproval
		decision.Reason = ToolReasonToolUnclassified
		decision.RiskLevel = ToolRiskUnclassified
	}
	return decision
}
