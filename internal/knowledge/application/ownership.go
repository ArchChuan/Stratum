package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// enforceOwnership applies the ownership matrix. owner may manage the whole
// tenant (including historical resources with empty created_by); admin may
// touch resources they created or that granted them editor rights (update
// semantics only — delete still requires creator/owner); every other role,
// resolution failure and empty actor is denied. Fail closed.
// editors is the granted editor set used for the admin-editor row of the
// matrix; pass nil to deny admin edits of foreign resources entirely.
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
		for _, id := range editors {
			if id == actorID {
				return nil
			}
		}
		return domain.ErrForbidden
	default:
		return domain.ErrForbidden
	}
}

// checkOwnership resolves the actor's tenant role and applies the matrix.
// Knowledge ports carry tenantID explicitly (no tenant in ctx), hence the
// parameter. A system actor in ctx bypasses ownership checks (evaluation
// worker path). resolver nil, resolution failure and empty actor all fail
// closed.
func (s *WorkspaceService) checkOwnership(ctx context.Context, tenantID, actorID, createdBy string, editors []string) error {
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
	return enforceOwnership(role, actorID, createdBy, editors)
}
