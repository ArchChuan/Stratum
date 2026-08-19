package infrastructure

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// insertPlatformAuditTx writes a public-catalog audit row in the same
// transaction as the public provider/model mutation.
func insertPlatformAuditTx(
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
	_, err := tx.Exec(ctx,
		`INSERT INTO public.platform_resource_change_audits
		 (id, scope, resource_kind, resource_id, operation, actor_id, actor_tenant_id,
		  actor_type, source, proposal_id, before_projection, after_projection)
		 VALUES ($1,'platform',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		eventID, ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, tenant,
		ev.ActorType, ev.Source, ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert platform audit: %w", err)
	}
	return nil
}
