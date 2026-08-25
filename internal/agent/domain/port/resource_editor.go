package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// ResourceEditorRepo persists the shared resource_editors table for agent
// resources (tenant-scoped). Create/update/delete writes are embedded in the
// AgentRepo write methods so editors change in the same transaction as the
// business row; this port covers the standalone read and the replace used by
// PUT /agents/:id/editors.
type ResourceEditorRepo interface {
	// ListEditors returns the editor ids of a resource, or an empty slice.
	ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error)
	// ReplaceEditors atomically swaps the editor set. Each editor must hold
	// role admin or owner at write time (checked inside the transaction,
	// fail closed); a non-eligible id returns domain.ErrEditorNotEligible.
	// audit, when non-nil, is written in the SAME transaction (audit failure
	// rolls the editor change back).
	ReplaceEditors(ctx context.Context, tenantID, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error
	// AddEditorForKind appends a single editor for an arbitrary resource kind
	// in the shared resource_editors table (idempotent on duplicates via the
	// composite primary key). It powers the grant_editor approval: agent and
	// skill whitelist grants both land here (kind "agent" / "skill"), with
	// eligibility re-validated inside the transaction (member+, fail closed).
	AddEditorForKind(ctx context.Context, tenantID, kind, resourceID, editorID, createdBy string) error
}
