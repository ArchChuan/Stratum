package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// OwnershipOp 区分写操作的破坏性等级，供权限矩阵决定 admin 是否天然放行。
type OwnershipOp int

const (
	// OpEdit 编辑内容（更新 draft、发布）。admin 天然放行。
	OpEdit OwnershipOp = iota
	// OpAccess 管理白名单/访问权限（SetEditors），即成员申请通道的审批入口。
	// admin 天然放行。
	OpAccess
	// OpDelete 破坏性删除（skill）。仅 creator 与 owner 可执行。
	OpDelete
)

// isGrantedEditor reports whether actorID appears in the granted editor set
// (可编辑人 whitelist)。member 走申请通道后据此获得编辑权。
func isGrantedEditor(actorID string, editors []string) bool {
	for _, id := range editors {
		if id == actorID {
			return true
		}
	}
	return false
}

// enforceOwnership applies the ownership matrix. owner may manage the whole
// tenant (including historical resources with empty created_by); admin is
// entitled to edit content and manage the whitelist by default (op != OpDelete),
// which also covers unowned built-in resources with empty created_by — delete
// stays creator/owner-only; member may only touch resources they were granted
// as editor (the whitelist grant, 可编辑人). editors is the granted editor set
// used for the member row; OpDelete paths pass nil and therefore still require
// creator/owner. Every other role, resolution failure and empty actor is
// denied. Fail closed.
func enforceOwnership(role, actorID, createdBy string, editors []string, op OwnershipOp) error {
	if actorID == "" {
		return domain.ErrForbidden
	}
	switch role {
	case "owner":
		return nil
	case "admin":
		// 管理员天然有资源编辑权限（OpEdit/OpAccess）；破坏性删除仍需 creator。
		if op == OpDelete && createdBy != actorID {
			return domain.ErrForbidden
		}
		return nil
	case "member":
		// Whitelist grant: a member may edit content they were granted as
		// editor; delete always stays locked to creator/owner.
		if op == OpDelete {
			return domain.ErrForbidden
		}
		if isGrantedEditor(actorID, editors) {
			return nil
		}
		return domain.ErrForbidden
	default:
		return domain.ErrForbidden
	}
}

// checkOwnership resolves the actor's tenant role and applies the matrix.
// A system actor in ctx bypasses ownership checks (evaluation worker path).
// resolver nil, resolution failure and empty actor all fail closed.
// editors is the granted editor set used for the member row of the matrix.
func (s *VersionService) checkOwnership(ctx context.Context, actorID, createdBy string, editors []string, op OwnershipOp) error {
	if reqctx.SystemActorFromContext(ctx) != "" {
		return nil
	}
	if actorID == "" || s.roles == nil {
		return domain.ErrForbidden
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	role, err := s.roles.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return domain.ErrForbidden
	}
	return enforceOwnership(role, actorID, createdBy, editors, op)
}
