package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/resourceaccess"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// resourceEditorKind identifies workflow rows in the shared resource_editors table.
const resourceEditorKind = "workflow"

func insertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string) error {
	return resourceaccess.InsertEditors(ctx, tx, tenantID, kind, resourceID, editorIDs, createdBy, domain.ErrEditorNotEligible)
}

func revalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string) error {
	return resourceaccess.RevalidateEditorAccess(ctx, tx, tenantID, kind, resourceID, actorID, domain.ErrForbidden)
}

// revalidateEditorIfActor re-checks the whitelist inside the write transaction
// when editorActor is non-empty (a whitelist member performing the write).
// owner/admin 路径 actor 为空串，跳过复查；缺租户上下文时 fail-closed。
func revalidateEditorIfActor(ctx context.Context, tx pgx.Tx, kind, resourceID, actorID string) error {
	if actorID == "" {
		return nil
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("workflow store: missing tenant context")
	}
	return revalidateEditorAccess(ctx, tx, tc.TenantID, kind, resourceID, actorID)
}

// PgWorkflowResourceEditorRepo manages the shared resource_editors table for
// workflow resources. Methods run through the tenant boundary encapsulation.
type PgWorkflowResourceEditorRepo struct {
	pool poolIface
}

var _ port.ResourceEditorRepo = (*PgWorkflowResourceEditorRepo)(nil)

func NewPgWorkflowResourceEditorRepo(pool *pgxpool.Pool) *PgWorkflowResourceEditorRepo {
	return &PgWorkflowResourceEditorRepo{pool: pool}
}

func (r *PgWorkflowResourceEditorRepo) ListEditors(ctx context.Context, tenantID, resourceID string) ([]string, error) {
	out := make([]string, 0)
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT editor_id FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2 ORDER BY created_at`,
			resourceEditorKind, resourceID,
		)
		if err != nil {
			return fmt.Errorf("list workflow editors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("scan workflow editor: %w", err)
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return []string{}, nil
	}
	return out, nil
}

func (r *PgWorkflowResourceEditorRepo) ReplaceEditors(ctx context.Context, tenantID, resourceID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, resourceID,
		); err != nil {
			return fmt.Errorf("clear workflow editors: %w", err)
		}
		if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, resourceID, editorIDs, createdBy); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

func (r *PgWorkflowResourceEditorRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if ok && tc.TenantID != tenantID {
		return fmt.Errorf("workflow editor repo: tenant context mismatch")
	}
	if !ok {
		ctx = pgstore.WithTenant(ctx, &pgstore.TenantContext{TenantID: tenantID})
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tenantID, fn)
}
