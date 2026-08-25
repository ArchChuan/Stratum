package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// isGrantedEditor reports whether actorID appears in the granted editor set
// (可编辑人 whitelist). Shared by the admin and member matrix rows.
func isGrantedEditor(actorID string, editors []string) bool {
	for _, id := range editors {
		if id == actorID {
			return true
		}
	}
	return false
}

// enforceOwnership applies the ownership matrix. owner may manage the whole
// tenant (including historical resources with empty created_by); admin may
// only touch resources they created, or — for updates only — resources they
// were granted as editor; member may only touch resources they were granted
// as editor (the whitelist grant, 可编辑人). editors is only honored on the
// update path: delete/SetEditors pass nil and therefore still require
// creator/owner. Every other role, resolution failure and empty actor is
// denied. Fail closed.
func enforceOwnership(role, actorID, createdBy string, editors []string) error {
	if actorID == "" {
		return domain.ErrForbidden
	}
	switch role {
	case "owner":
		return nil
	case "admin":
		if createdBy == actorID || isGrantedEditor(actorID, editors) {
			return nil
		}
		return domain.ErrForbidden
	case "member":
		// Whitelist grant: a member may update drafts they were granted as
		// editor. delete/SetEditors pass nil editors, so those ops stay locked
		// to creator/owner.
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
// editors is the granted editor set used for the admin-editor row of the
// matrix; pass nil to deny admin edits of foreign resources entirely.
func (s *VersionService) checkOwnership(ctx context.Context, actorID, createdBy string, editors []string) error {
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
	return enforceOwnership(role, actorID, createdBy, editors)
}
