package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

type stubToolUserScopeResolver struct {
	scope port.ToolUserScope
	err   error
}

func (s stubToolUserScopeResolver) ResolveToolUserScope(
	context.Context, string, string, string, string,
) (port.ToolUserScope, error) {
	return s.scope, s.err
}

func TestToolAuthorizerAllowsActiveMemberWithinAgentScope(t *testing.T) {
	authorizer := NewToolAuthorizer(stubToolUserScopeResolver{
		scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
	})

	decision := authorizer.Authorize(context.Background(), ToolAuthorizationInput{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
		AgentAllowsTool: true, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
	})

	require.Equal(t, domain.ToolAuthorizationAllow, decision.Effect)
	require.Equal(t, domain.ToolReasonRiskAllowed, decision.Reason)
}

func TestToolAuthorizerFailsClosedForUserScope(t *testing.T) {
	tests := []struct {
		name     string
		input    ToolAuthorizationInput
		resolver stubToolUserScopeResolver
		reason   domain.ToolAuthorizationReason
	}{
		{
			name: "missing tenant context",
			input: ToolAuthorizationInput{
				UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}},
			reason:   domain.ToolReasonTenantContextMissing,
		},
		{
			name: "inactive user",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{AllowsTool: true}},
			reason:   domain.ToolReasonUserInactive,
		},
		{
			name: "user policy denies",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true}},
			reason:   domain.ToolReasonUserPermissionDenied,
		},
		{
			name: "resolver error",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{err: errors.New("iam unavailable")},
			reason:   domain.ToolReasonPolicyLookupFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := NewToolAuthorizer(tt.resolver).Authorize(context.Background(), tt.input)

			require.Equal(t, domain.ToolAuthorizationDeny, decision.Effect)
			require.Equal(t, tt.reason, decision.Reason)
		})
	}
}

func TestToolAuthorizerUserScopeCannotExpandAgentAllowlist(t *testing.T) {
	authorizer := NewToolAuthorizer(stubToolUserScopeResolver{
		scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
	})

	decision := authorizer.Authorize(context.Background(), ToolAuthorizationInput{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:delete",
		AgentAllowsTool: false, ActiveSkillAllows: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
	})

	require.Equal(t, domain.ToolAuthorizationDeny, decision.Effect)
	require.Equal(t, domain.ToolReasonToolNotAllowlisted, decision.Reason)
}
