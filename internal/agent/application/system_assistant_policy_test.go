package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

type diagnosticRoleResolverStub struct {
	role string
	err  error
}

func (s diagnosticRoleResolverStub) ResolveTenantRole(context.Context, string, string) (string, error) {
	return s.role, s.err
}

func TestDiagnosticScopePolicy(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		requested domain.DiagnosticScope
		want      domain.DiagnosticScope
		wantErr   error
	}{
		{name: "member defaults to self", role: "member", want: domain.DiagnosticScopeSelf},
		{name: "member tenant request narrows to self", role: "member", requested: domain.DiagnosticScopeTenant, want: domain.DiagnosticScopeSelf},
		{name: "admin defaults to tenant", role: "admin", want: domain.DiagnosticScopeTenant},
		{name: "owner defaults to tenant", role: "owner", want: domain.DiagnosticScopeTenant},
		{name: "admin self request stays self", role: "admin", requested: domain.DiagnosticScopeSelf, want: domain.DiagnosticScopeSelf},
		{name: "unknown role forbidden", role: "guest", wantErr: domain.ErrDiagnosticForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AuthorizeDiagnosticRequest(context.Background(), diagnosticRoleResolverStub{role: tt.role}, domain.DiagnosticRequest{
				TenantID: "tenant-1", UserID: "user-1", Scope: tt.requested, Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent},
			})
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got.Scope)
		})
	}
}

func TestDiagnosticScopePolicyFailsClosed(t *testing.T) {
	tests := []domain.DiagnosticRequest{
		{UserID: "user-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}},
		{TenantID: "tenant-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}},
		{TenantID: "tenant-1", UserID: "user-1"},
		{TenantID: "tenant-1", UserID: "user-1", Areas: []domain.DiagnosticArea{"unknown"}},
		{TenantID: "tenant-1", UserID: "user-1", Scope: "global", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}},
	}
	for _, req := range tests {
		_, err := AuthorizeDiagnosticRequest(context.Background(), diagnosticRoleResolverStub{role: "owner"}, req)
		require.ErrorIs(t, err, domain.ErrDiagnosticForbidden)
	}

	_, err := AuthorizeDiagnosticRequest(context.Background(), diagnosticRoleResolverStub{err: errors.New("raw IAM failure")}, domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "user-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent},
	})
	require.ErrorIs(t, err, domain.ErrDiagnosticForbidden)
	require.NotContains(t, err.Error(), "raw IAM failure")
}
