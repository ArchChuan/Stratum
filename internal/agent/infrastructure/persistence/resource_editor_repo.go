package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// PgResourceEditorRepo manages the shared resource_editors table for agent
// resources. Editor writes are tenant-scoped like the business rows they
// accompany.
type PgResourceEditorRepo struct {
	pool poolIface
}

var _ port.ResourceEditorRepo = (*PgResourceEditorRepo)(nil)

// NewPgResourceEditorRepo builds the editor repository over the tenant pool.
func NewPgResourceEditorRepo(pool *pgxpool.Pool) *PgResourceEditorRepo {
	return &PgResourceEditorRepo{pool: pool}
}

func (r *PgResourceEditorRepo) execTenant(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("resource editor repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tc.TenantID, fn)
}

// ListEditors returns the editor ids of an agent resource, or an empty slice.
func (r *PgResourceEditorRepo) ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error) {
	out := make([]string, 0)
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
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
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, resourceID,
		); err != nil {
			return fmt.Errorf("replace editors: clear: %w", err)
		}
		if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, resourceID, editorIDs, createdBy); err != nil {
			return err
		}
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
}

// AddEditorForKind inserts a single editor for an arbitrary resource kind in
// the shared resource_editors table. It powers the grant_editor approval:
// skill whitelist grants are written here by the agent module's repo (kind
// "skill") reusing the same table the skill module reads, and knowledge
// workspace editors reuse kind "knowledge". Eligibility is re-validated
// inside the transaction (member+, see editorEligible); insert is idempotent
// on duplicates via the composite primary key.
func (r *PgResourceEditorRepo) AddEditorForKind(ctx context.Context, tenantID, kind, resourceID, editorID, createdBy string) error {
	if editorID == "" {
		return fmt.Errorf("add editor: empty editor id")
	}
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		eligible, err := editorEligible(ctx, tx, tenantID, editorID)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("%w: user %s", domain.ErrEditorNotEligible, editorID)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_editors (resource_kind, resource_id, editor_id, created_by)
			 VALUES ($1,$2,$3,$4)
			 ON CONFLICT (resource_kind, resource_id, editor_id) DO NOTHING`,
			kind, resourceID, editorID, createdBy); err != nil {
			return fmt.Errorf("add editor %s: %w", editorID, err)
		}
		return nil
	})
}
