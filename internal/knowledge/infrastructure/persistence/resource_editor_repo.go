package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// PgResourceEditorRepo manages the shared resource_editors table for
// knowledge workspaces. Editor writes are tenant-scoped like the business
// rows they accompany. tenantID is explicit because knowledge repos receive
// it as a parameter rather than from the tenant context.
type PgResourceEditorRepo struct {
	pool poolIface
}

var _ port.ResourceEditorRepo = (*PgResourceEditorRepo)(nil)

// NewPgResourceEditorRepo builds the editor repository over the tenant pool.
func NewPgResourceEditorRepo(pool poolIface) *PgResourceEditorRepo {
	return &PgResourceEditorRepo{pool: pool}
}

// ListEditors returns the editor ids of a workspace, or an empty slice.
func (r *PgResourceEditorRepo) ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error) {
	out := make([]string, 0)
	err := execTenant(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT editor_id FROM resource_editors
			  WHERE resource_kind=$1 AND resource_id=$2
			  ORDER BY created_at`,
			resourceEditorKind, resourceID,
		)
		if err != nil {
			return fmt.Errorf("list editors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan editor: %w", err)
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// ReplaceEditors atomically swaps the editor set. Each editor must hold role
// admin or owner at write time (checked inside the transaction, fail closed);
// a non-eligible id returns domain.ErrEditorNotEligible. The audit event,
// when non-nil, is written in the same transaction.
func (r *PgResourceEditorRepo) ReplaceEditors(ctx context.Context, tenantID, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
	return execTenant(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, resourceID,
		); err != nil {
			return fmt.Errorf("replace editors: clear: %w", err)
		}
		if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, resourceID, editorIDs, createdBy); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, tenantID, audit)
	})
}
