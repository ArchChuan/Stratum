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
		AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
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
				AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}},
			reason:   domain.ToolReasonTenantContextMissing,
		},
		{
			name: "inactive user",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{AllowsTool: true}},
			reason:   domain.ToolReasonUserInactive,
		},
		{
			name: "user policy denies",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
			},
			resolver: stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true}},
			reason:   domain.ToolReasonUserPermissionDenied,
		},
		{
			name: "resolver error",
			input: ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
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
		AgentAllowsTool: false, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
	})

	require.Equal(t, domain.ToolAuthorizationDeny, decision.Effect)
	require.Equal(t, domain.ToolReasonToolNotAllowlisted, decision.Reason)
}

func TestToolAuthorizerSharedRiskModelAppliesToAllAgents(t *testing.T) {
	// 系统助手等同化后：所有 agent（含平台助手 seed 行）共享同一授权模型。
	// 未配置 policy（policy_resolved=false）一律 require_approval（含 read）；
	// 管理员经 SetToolPolicy 配置 risk_level 后 read/write_reversible/destructive
	// 均直接放行（配置即授权）。
	tests := []struct {
		name           string
		risk           domain.ToolRiskLevel
		policyResolved bool
		effect         domain.ToolAuthorizationEffect
		reason         domain.ToolAuthorizationReason
	}{
		{name: "read configured runs automatically", risk: domain.ToolRiskRead, policyResolved: true, effect: domain.ToolAuthorizationAllow, reason: domain.ToolReasonRiskAllowed},
		{name: "write reversible configured runs automatically", risk: domain.ToolRiskWriteReversible, policyResolved: true, effect: domain.ToolAuthorizationAllow, reason: domain.ToolReasonRiskAllowed},
		{name: "destructive configured runs automatically", risk: domain.ToolRiskDestructive, policyResolved: true, effect: domain.ToolAuthorizationAllow, reason: domain.ToolReasonRiskAllowed},
		{name: "unconfigured read requires approval", risk: domain.ToolRiskRead, policyResolved: false, effect: domain.ToolAuthorizationRequireApproval, reason: domain.ToolReasonPolicyLookupFailed},
		{name: "unclassified requires approval", risk: domain.ToolRiskUnclassified, policyResolved: true, effect: domain.ToolAuthorizationRequireApproval, reason: domain.ToolReasonToolUnclassified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := NewToolAuthorizer(stubToolUserScopeResolver{
				scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
			})

			decision := authorizer.Authorize(context.Background(), ToolAuthorizationInput{
				TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
				AgentAllowsTool: true, PolicyResolved: tt.policyResolved, RiskLevel: tt.risk,
			})

			require.Equal(t, tt.effect, decision.Effect)
			require.Equal(t, tt.reason, decision.Reason)
		})
	}
}

func TestToolAuthorizerFailsClosedOnPolicyLookupFailure(t *testing.T) {
	authorizer := NewToolAuthorizer(stubToolUserScopeResolver{
		err: errors.New("iam unavailable"),
	})

	decision := authorizer.Authorize(context.Background(), ToolAuthorizationInput{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:get",
		AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskRead,
	})

	require.Equal(t, domain.ToolAuthorizationDeny, decision.Effect)
	require.Equal(t, domain.ToolReasonPolicyLookupFailed, decision.Reason)
}

func TestToolAuthorizerOrdinaryAgentModelUnchanged(t *testing.T) {
	authorizer := NewToolAuthorizer(stubToolUserScopeResolver{
		scope: port.ToolUserScope{UserActive: true, AllowsTool: true},
	})

	decision := authorizer.Authorize(context.Background(), ToolAuthorizationInput{
		TenantID: "tenant-1", UserID: "user-1", AgentID: "agent-1", ToolID: "mcp:orders:create",
		AgentAllowsTool: true, PolicyResolved: true, RiskLevel: domain.ToolRiskWriteReversible,
	})

	require.Equal(t, domain.ToolAuthorizationAllow, decision.Effect)
	require.Equal(t, domain.ToolReasonRiskAllowed, decision.Reason)
}
