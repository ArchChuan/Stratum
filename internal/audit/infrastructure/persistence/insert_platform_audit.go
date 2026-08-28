package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// InsertPlatformAuditTx writes a public-catalog audit row in the same
// transaction as the public provider/model/tenant mutation. It is the shared
// execution scaffold for platform_resource_change_audits: llmgateway and iam
// both call it so the INSERT column contract stays in lockstep with
// PlatformChangeAuditInsertSQL (asserted by change_audit_insert_test.go).
func InsertPlatformAuditTx(
	ctx context.Context,
	tx pgx.Tx,
	actorTenantID string,
	ev *auditdomain.ResourceChangeAuditEvent,
) error {
	if ev == nil {
		return nil
	}
	ev = ev.Normalized()
	eventID := ev.EventID
	if eventID == "" {
		eventID = uuid.Must(uuid.NewV7()).String()
	}
	var tenant any
	if actorTenantID != "" {
		tenant = actorTenantID
	}
	_, err := tx.Exec(ctx, auditdomain.PlatformChangeAuditInsertSQL,
		eventID, ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, tenant,
		ev.ActorType, ev.Source, ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert platform audit: %w", err)
	}
	return nil
}
