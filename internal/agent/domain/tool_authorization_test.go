package domain

import (
	"math/rand"
	"testing"
)

func TestAuthorizeTool(t *testing.T) {
	base := ToolAuthorizationRequest{
		TenantID:          "tenant-1",
		UserID:            "user-1",
		ToolID:            "mcp:orders:get_order",
		UserActive:        true,
		UserAllowsTool:    true,
		AgentAllowsTool:   true,
		PolicyResolved:    true,
		RiskLevel:         ToolRiskRead,
		ActiveSkillAllows: true,
	}

	tests := []struct {
		name   string
		mutate func(*ToolAuthorizationRequest)
		effect ToolAuthorizationEffect
		reason ToolAuthorizationReason
	}{
		{name: "missing tenant", mutate: func(r *ToolAuthorizationRequest) { r.TenantID = "" }, effect: ToolAuthorizationDeny, reason: ToolReasonTenantContextMissing},
		{name: "inactive user", mutate: func(r *ToolAuthorizationRequest) { r.UserActive = false }, effect: ToolAuthorizationDeny, reason: ToolReasonUserInactive},
		{name: "user denied", mutate: func(r *ToolAuthorizationRequest) { r.UserAllowsTool = false }, effect: ToolAuthorizationDeny, reason: ToolReasonUserPermissionDenied},
		{name: "agent denied", mutate: func(r *ToolAuthorizationRequest) { r.AgentAllowsTool = false }, effect: ToolAuthorizationDeny, reason: ToolReasonToolNotAllowlisted},
		{name: "active skill denied", mutate: func(r *ToolAuthorizationRequest) { r.ActiveSkill = true; r.ActiveSkillAllows = false }, effect: ToolAuthorizationDeny, reason: ToolReasonSkillScopeExceeded},
		{name: "policy lookup failed", mutate: func(r *ToolAuthorizationRequest) { r.PolicyResolved = false }, effect: ToolAuthorizationRequireApproval, reason: ToolReasonPolicyLookupFailed},
		{name: "unclassified", mutate: func(r *ToolAuthorizationRequest) { r.RiskLevel = ToolRiskUnclassified }, effect: ToolAuthorizationRequireApproval, reason: ToolReasonToolUnclassified},
		{name: "read", mutate: func(r *ToolAuthorizationRequest) { r.RiskLevel = ToolRiskRead }, effect: ToolAuthorizationAllow, reason: ToolReasonRiskAllowed},
		{name: "reversible write", mutate: func(r *ToolAuthorizationRequest) { r.RiskLevel = ToolRiskWriteReversible }, effect: ToolAuthorizationAllow, reason: ToolReasonRiskAllowed},
		{name: "destructive", mutate: func(r *ToolAuthorizationRequest) { r.RiskLevel = ToolRiskDestructive }, effect: ToolAuthorizationRequireApproval, reason: ToolReasonApprovalRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			tt.mutate(&req)

			decision := AuthorizeTool(req)

			if decision.Effect != tt.effect || decision.Reason != tt.reason {
				t.Fatalf("decision=(%q,%q), want (%q,%q)", decision.Effect, decision.Reason, tt.effect, tt.reason)
			}
		})
	}
}

func TestToolAuthorizationMonotonicity(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	risks := []ToolRiskLevel{ToolRiskRead, ToolRiskWriteReversible, ToolRiskDestructive, ToolRiskUnclassified}

	for i := 0; i < 1_000; i++ {
		base := ToolAuthorizationRequest{
			TenantID:          "tenant-1",
			UserID:            "user-1",
			ToolID:            "mcp:test:tool",
			UserActive:        true,
			UserAllowsTool:    true,
			AgentAllowsTool:   true,
			PolicyResolved:    true,
			RiskLevel:         risks[rng.Intn(len(risks))],
			ActiveSkillAllows: true,
		}
		baseline := AuthorizeTool(base)

		restricted := base
		switch rng.Intn(4) {
		case 0:
			restricted.UserActive = false
		case 1:
			restricted.UserAllowsTool = false
		case 2:
			restricted.AgentAllowsTool = false
		case 3:
			restricted.ActiveSkill = true
			restricted.ActiveSkillAllows = false
		}

		if authorizationRank(AuthorizeTool(restricted).Effect) > authorizationRank(baseline.Effect) {
			t.Fatalf("restriction expanded permission: base=%+v restricted=%+v", base, restricted)
		}
	}
}

func authorizationRank(effect ToolAuthorizationEffect) int {
	switch effect {
	case ToolAuthorizationAllow:
		return 2
	case ToolAuthorizationRequireApproval:
		return 1
	default:
		return 0
	}
}
