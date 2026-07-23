package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

func AuthorizeDiagnosticRequest(
	ctx context.Context, resolver port.TenantRoleResolver, req domain.DiagnosticRequest,
) (domain.DiagnosticRequest, error) {
	if resolver == nil || req.TenantID == "" || req.UserID == "" || len(req.Areas) == 0 {
		return domain.DiagnosticRequest{}, domain.ErrDiagnosticForbidden
	}
	if req.Scope != "" && req.Scope != domain.DiagnosticScopeSelf && req.Scope != domain.DiagnosticScopeTenant {
		return domain.DiagnosticRequest{}, domain.ErrDiagnosticForbidden
	}
	for _, area := range req.Areas {
		if !area.Valid() {
			return domain.DiagnosticRequest{}, domain.ErrDiagnosticForbidden
		}
	}
	role, err := resolver.ResolveTenantRole(ctx, req.TenantID, req.UserID)
	if err != nil {
		return domain.DiagnosticRequest{}, domain.ErrDiagnosticForbidden
	}
	switch role {
	case "member":
		req.Scope = domain.DiagnosticScopeSelf
	case "admin", "owner":
		if req.Scope == "" {
			req.Scope = domain.DiagnosticScopeTenant
		}
	default:
		return domain.DiagnosticRequest{}, domain.ErrDiagnosticForbidden
	}
	return req, nil
}
