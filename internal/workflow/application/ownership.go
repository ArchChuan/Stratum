package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// OwnershipOp 区分写操作的破坏性等级：白名单成员与 admin 的 Delete 语义不同。
type OwnershipOp int

const (
	// OpEdit 编辑内容（更新 draft、发布）。admin 天然放行。
	OpEdit OwnershipOp = iota
	// OpAccess 管理白名单/访问权限（SetEditors），即成员申请通道的审批入口。
	// admin 天然放行。
	OpAccess
	// OpDelete 破坏性删除（workflow）。仅 creator 与 owner 可执行。
	OpDelete
	// OpRollback 回退生效指针到历史版本（Rollback）。破坏性等价 Delete：
	// owner 放行，admin 放行（无需本人为 creator），白名单 member 一律拒绝。
	OpRollback
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

// enforceOwnership applies the workflow ownership matrix. Fail closed on
// unknown role or empty actor. Delete/Rollback stay with owner / admin
// （Rollback 不要求 admin 本人为 creator）。
// 见 spec 矩阵：owner 全放行；admin 除 Delete 需 createdBy==actorID 外放行；
// member 仅白名单成员且非 Delete/Rollback；其余一律 403。
func enforceOwnership(role, actorID, createdBy string, editors []string, op OwnershipOp) error {
	if actorID == "" {
		return domain.ErrForbidden
	}
	switch role {
	case "owner":
		return nil
	case "admin":
		// admin 中仅破坏性删除要求 creator 本人；Rollback 属运维操作，不要求。
		if op == OpDelete && createdBy != actorID {
			return domain.ErrForbidden
		}
		return nil
	case "member":
		// Rollback 与 Delete 同属高破坏性：白名单 member 也无权回退版本。
		if op == OpDelete || op == OpRollback {
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
// Workflow ports carry tenantID explicitly (service boundary has no tenant
// context key), hence the parameter. SystemActorFromContext bypasses checks
// (worker/scheduler path). resolver nil, resolution failure and empty actor
// all fail closed.
func (s *DefinitionService) checkOwnership(ctx context.Context, tenantID, actorID, createdBy string, editors []string, op OwnershipOp) error {
	if reqctx.SystemActorFromContext(ctx) != "" {
		return nil
	}
	if actorID == "" || s.roles == nil {
		return domain.ErrForbidden
	}
	role, err := s.roles.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return domain.ErrForbidden
	}
	return enforceOwnership(role, actorID, createdBy, editors, op)
}

// resolveUpdateActor applies the ownership matrix on the Update/Publish path.
// owner/creator-admin pass with empty editorActor（写事务不复查白名单）；
// 白名单成员 pass with editorActor set，store 在写事务内复查关闭 TOCTOU。
// 缺 editorRepo / 白名单查询失败 / 未授权一律 fail closed。
func (s *DefinitionService) resolveUpdateActor(ctx context.Context, tenantID, actorID string, current *domain.Definition) (string, error) {
	if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, nil, OpEdit); err == nil {
		return "", nil
	}
	if s.editors == nil {
		return "", domain.ErrForbidden
	}
	editors, err := s.editors.ListEditors(ctx, tenantID, current.ID)
	if err != nil {
		return "", err
	}
	if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, editors, OpEdit); err != nil {
		return "", err
	}
	return actorID, nil
}
