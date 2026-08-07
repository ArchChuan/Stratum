package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// SkillResourceEditorRepo persists the shared resource_editors table for skill
// resources (tenant-scoped). Create/update/delete writes are embedded in the
// VersionRepo write methods so editors change in the same transaction as the
// business row; this port covers the standalone read and the replace used by
// PUT /skills/:id/editors.
type SkillResourceEditorRepo interface {
	// ListEditors returns the editor ids of a resource, or an empty slice.
	ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error)
	// ReplaceEditors atomically swaps the editor set. Each editor must hold
	// role admin or owner at write time (checked inside the transaction,
	// fail closed); a non-eligible id returns domain.ErrEditorNotEligible.
	// audit, when non-nil, is written in the SAME transaction (audit failure
	// rolls the editor change back).
	ReplaceEditors(ctx context.Context, tenantID, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error
}
