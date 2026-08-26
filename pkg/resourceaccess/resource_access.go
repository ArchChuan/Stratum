// Package resourceaccess provides the shared transaction-scoped SQL helpers
// for the resource editor whitelist and the resource change audit row.
//
// These tables (public.tenant_members, resource_editors,
// resource_change_audits) are platform-level shared storage, not the business
// of any single bounded context. Previously each of agent/knowledge/skill/mcp
// owned a private copy of editorEligible/insertEditors/revalidateEditorAccess
// and each of agent/knowledge/skill/mcp/evaluation/workflow owned a private
// copy of insertChangeAudit — the copies drifted (the agent editorEligible
// alone grew a role whitelist; see #475) and every shared-logic fix had to be
// replayed by hand. This package is the single implementation; callers keep a
// thin wrapper so their domain error sentinels still propagate.
//
// pkg/ must not import internal/, so the change-audit insert SQL and the
// domain error sentinels are passed in by the caller (auditdomain.
// ChangeAuditInsertSQL and the per-context ErrEditorNotEligible/ErrForbidden).
package resourceaccess

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// allowedEditorRoles is the editor-eligibility role whitelist. The agent
// bounded context was the first to enforce it (#475); it is now the single
// semantic baseline shared by every resource context, so a future role
// addition cannot silently diverge per context (fail closed by default).
var allowedEditorRoles = []string{"admin", "owner", "member"}

// EditorEligible reports whether userID is an active tenant member holding a
// whitelisted role. Fail closed on any lookup error. public.tenant_members is
// schema-qualified: the transaction search_path points at the tenant schema.
func EditorEligible(ctx context.Context, tx pgx.Tx, tenantID, userID string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM public.tenant_members
			WHERE tenant_id=$1 AND user_id=$2 AND role = ANY($3))`,
		tenantID, userID, allowedEditorRoles,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("editor role check: %w", err)
	}
	return ok, nil
}

// InsertEditors validates and persists the editor set inside the write
// transaction. A non-eligible id fails the whole transaction (fail closed),
// so a forged editor can never be created alongside the resource.
// notEligibleErr is the caller's domain sentinel (per-context
// ErrEditorNotEligible), wrapped so errors.Is still matches.
func InsertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string, notEligibleErr error) error {
	for _, id := range editorIDs {
		eligible, err := EditorEligible(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("%w: user %s", notEligibleErr, id)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_editors (resource_kind, resource_id, editor_id, created_by)
			 VALUES ($1,$2,$3,$4)`,
			kind, resourceID, id, createdBy,
		); err != nil {
			return fmt.Errorf("insert editor %s: %w", id, err)
		}
	}
	return nil
}

// RevalidateEditorAccess re-checks, inside the write transaction, that the
// actor still qualifies as an editor of this resource: whitelisted tenant
// membership AND presence in resource_editors. Both checks share the
// transaction with the business UPDATE, closing the check-then-write TOCTOU
// window. forbiddenErr is the caller's domain sentinel (per-context
// ErrForbidden).
func RevalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string, forbiddenErr error) error {
	eligible, err := EditorEligible(ctx, tx, tenantID, actorID)
	if err != nil {
		return err
	}
	if !eligible {
		return forbiddenErr
	}
	var present bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resource_editors
			WHERE resource_kind=$1 AND resource_id=$2 AND editor_id=$3)`,
		kind, resourceID, actorID,
	).Scan(&present); err != nil {
		return fmt.Errorf("editor presence check: %w", err)
	}
	if !present {
		return forbiddenErr
	}
	return nil
}

// ChangeAudit describes one resource change audit row. Fields mirror the
// audit domain event model; callers convert from
// internal/audit/domain.ResourceChangeAuditEvent because pkg/ must not
// import internal/.
type ChangeAudit struct {
	ResourceKind string
	ResourceID   string
	Operation    string
	ActorID      string
	ActorType    string
	Source       string
	ProposalID   string
	Before       json.RawMessage
	After        json.RawMessage
}

// InsertChangeAudit persists one audit row inside the business transaction; an
// audit failure rolls the business change back (fail closed). insertSQL is the
// caller-provided shared column contract (auditdomain.ChangeAuditInsertSQL).
// An incomplete event (missing kind/id/operation) is a caller bug and fails
// the transaction.
func InsertChangeAudit(ctx context.Context, tx pgx.Tx, tenantID, insertSQL string, ev ChangeAudit) error {
	if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
		return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
			ev.ResourceKind, ev.ResourceID, ev.Operation)
	}
	_, err := tx.Exec(ctx, insertSQL,
		uuid.Must(uuid.NewV7()).String(), tenantID,
		ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
		ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
	}
	return nil
}
