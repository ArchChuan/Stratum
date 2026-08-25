package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// enforceOwnership applies the ownership matrix. owner may manage the whole
// tenant (including historical resources with empty created_by); admin may
// manage resources they created, or — for updates only — resources they were
// granted as editor; whitelisted members (editors) likewise pass updates but
// nothing else (editors is only honored on the update path; delete still
// requires creator/owner). Every other role, resolution failure and empty
// actor is denied. Fail closed.
func enforceOwnership(role, actorID, createdBy string, editors []string) error {
	if actorID == "" {
		return domain.ErrForbidden
	}
	switch role {
	case "owner":
		return nil
	case "admin":
		if createdBy == actorID {
			return nil
		}
	}
	// 非 owner/admin 创建者的角色（admin 非创建者 / member 白名单成员）
	// 命中 editors 即放行（仅更新路径，删除仍由 owner/creator 独享——
	// 调用方 delete 路径传 nil editors）。
	for _, id := range editors {
		if id == actorID {
			return nil
		}
	}
	return domain.ErrForbidden
}

// checkOwnership resolves the actor's tenant role and applies the matrix.
// A system actor in ctx bypasses ownership checks (evaluation worker path).
// resolver nil, resolution failure and empty actor all fail closed.
// editors is the granted editor set used for the admin-editor row of the
// matrix; pass nil to deny admin edits of foreign resources entirely.
func (s *AgentService) checkOwnership(ctx context.Context, actorID, createdBy string, editors []string) error {
	if reqctx.SystemActorFromContext(ctx) != "" {
		return nil
	}
	if actorID == "" || s.deps.TenantRoleResolver == nil {
		return domain.ErrForbidden
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	role, err := s.deps.TenantRoleResolver.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return domain.ErrForbidden
	}
	return enforceOwnership(role, actorID, createdBy, editors)
}
