// Package persistence provides the PostgreSQL adapter for the audit context.
// Audit events live in the public schema (not per-tenant) because they are
// a platform-level concern shared across tenants.
package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/safetext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgAuditRepo persists audit events in the public.audit_events table.
type PgAuditRepo struct {
	pool *pgxpool.Pool
}

// NewPgAuditRepo constructs a PostgreSQL-backed audit repository.
func NewPgAuditRepo(pool *pgxpool.Pool) *PgAuditRepo {
	return &PgAuditRepo{pool: pool}
}

// InsertBatch writes multiple events in a single COPY-friendly INSERT.
func (r *PgAuditRepo) InsertBatch(ctx context.Context, events []domain.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for i := range events {
		e := &events[i]
		// Redact credentials from JSON snapshots before persisting.
		before := redactJSON(e.Before)
		after := redactJSON(e.After)
		batch.Queue(
			`INSERT INTO public.audit_events
			 (id, tenant_id, actor_id, actor_type, action, resource_type, resource_id,
			  before, after, request_id, trace_id, risk_level, outcome, occurred_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			e.ID, e.TenantID, e.Actor.ActorID, string(e.Actor.ActorType),
			e.Action, e.ResourceType, e.ResourceID,
			before, after, e.RequestID, e.TraceID,
			e.RiskLevel, e.Outcome, e.OccurredAt,
		)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range events {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("audit: batch insert: %w", err)
		}
	}
	return nil
}

// scanAuditEvent unmarshals a single row into a domain event.
func scanAuditEvent(rows pgx.Rows) (domain.AuditEvent, error) {
	var e domain.AuditEvent
	var actorType string
	if err := rows.Scan(
		&e.ID, &e.TenantID, &e.Actor.ActorID, &actorType,
		&e.Action, &e.ResourceType, &e.ResourceID,
		&e.Before, &e.After, &e.RequestID, &e.TraceID,
		&e.RiskLevel, &e.Outcome, &e.OccurredAt,
	); err != nil {
		return e, fmt.Errorf("audit: scan: %w", err)
	}
	e.Actor.ActorType = domain.AuditActorType(actorType)
	return e, nil
}

// buildAuditFilter returns a WHERE clause and parameter list.
func buildAuditFilter(f domain.AuditFilter) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	argIdx := 1

	appendEq := func(col string, val any) {
		clauses = append(clauses, fmt.Sprintf("%s = $%d", col, argIdx))
		args = append(args, val)
		argIdx++
	}
	if f.TenantID != "" {
		appendEq("tenant_id", f.TenantID)
	}
	if f.ActorID != "" {
		appendEq("actor_id", f.ActorID)
	}
	if f.ResourceType != "" {
		appendEq("resource_type", f.ResourceType)
	}
	if f.ResourceID != "" {
		appendEq("resource_id", f.ResourceID)
	}
	if f.RiskLevel != "" {
		appendEq("risk_level", f.RiskLevel)
	}
	if f.Action != "" {
		appendEq("action", f.Action)
	}
	if f.Outcome != "" {
		appendEq("outcome", f.Outcome)
	}
	if !f.From.IsZero() {
		appendEq("occurred_at >=", f.From)
	}
	if !f.To.IsZero() {
		appendEq("occurred_at <=", f.To)
	}
	return strings.Join(clauses, " AND "), args
}

// Query returns events matching the filter, newest first.
func (r *PgAuditRepo) Query(ctx context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error) {
	where, args := buildAuditFilter(f)
	argIdx := len(args) + 1

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args = append(args, limit, f.Offset)

	query := fmt.Sprintf(
		`SELECT id, tenant_id, actor_id, actor_type, action, resource_type, resource_id,
		        before, after, request_id, trace_id, risk_level, outcome, occurred_at
		 FROM public.audit_events
		 WHERE %s
		 ORDER BY occurred_at DESC
		 LIMIT $%d OFFSET $%d`,
		where, argIdx, argIdx+1,
	)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer rows.Close()

	var events []domain.AuditEvent
	for rows.Next() {
		e, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetByID returns a single event or nil.
func (r *PgAuditRepo) GetByID(ctx context.Context, id string) (*domain.AuditEvent, error) {
	var e domain.AuditEvent
	var actorType string
	err := r.pool.QueryRow(ctx,
		`SELECT id, tenant_id, actor_id, actor_type, action, resource_type, resource_id,
		        before, after, request_id, trace_id, risk_level, outcome, occurred_at
		 FROM public.audit_events WHERE id = $1`, id,
	).Scan(
		&e.ID, &e.TenantID, &e.Actor.ActorID, &actorType,
		&e.Action, &e.ResourceType, &e.ResourceID,
		&e.Before, &e.After, &e.RequestID, &e.TraceID,
		&e.RiskLevel, &e.Outcome, &e.OccurredAt,
	)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: get by id: %w", err)
	}
	e.Actor.ActorType = domain.AuditActorType(actorType)
	return &e, nil
}

// DeleteOlderThan removes events before the given time (retention policy).
func (r *PgAuditRepo) DeleteOlderThan(ctx context.Context, before time.Time) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM public.audit_events WHERE occurred_at < $1`, before)
	if err != nil {
		return fmt.Errorf("audit: delete older than: %w", err)
	}
	return nil
}

func redactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	return json.RawMessage(safetext.RedactCredentials(string(raw)))
}
