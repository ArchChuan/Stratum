package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubTenantMemberRoleService struct {
	role string
	err  error
}

func (s stubTenantMemberRoleService) GetMemberRole(context.Context, string, string) (string, error) {
	return s.role, s.err
}

func TestAgentToolUserScopeResolverRecognizesActiveTenantRoles(t *testing.T) {
	for _, role := range []string{"member", "admin", "owner"} {
		t.Run(role, func(t *testing.T) {
			resolver := agentToolUserScopeResolver{members: stubTenantMemberRoleService{role: role}}

			scope, err := resolver.ResolveToolUserScope(
				context.Background(), "tenant-1", "user-1", "agent-1", "mcp:orders:get",
			)

			require.NoError(t, err)
			require.True(t, scope.UserActive)
			require.True(t, scope.AllowsTool)
		})
	}
}

func TestAgentToolUserScopeResolverFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		service tenantMemberRoleService
	}{
		{name: "missing service"},
		{name: "lookup failure", service: stubTenantMemberRoleService{err: errors.New("database unavailable")}},
		{name: "unknown role", service: stubTenantMemberRoleService{role: "suspended"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := agentToolUserScopeResolver{members: tt.service}

			scope, err := resolver.ResolveToolUserScope(
				context.Background(), "tenant-1", "user-1", "agent-1", "mcp:orders:get",
			)

			require.Error(t, err)
			require.False(t, scope.UserActive)
			require.False(t, scope.AllowsTool)
		})
	}
}
