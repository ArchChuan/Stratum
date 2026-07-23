package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgStore struct{ pool *pgxpool.Pool }

const runSelectColumns = `SELECT id,definition_id,version_id,version_no,status,snapshot_json,input_json,
	output_text,error_message,idempotency_key,request_hash,generation,scheduler_owner,lease_expires_at,
	pause_reason,cancel_reason,manual_reason,created_by,created_at,updated_at,started_at,finished_at
	FROM workflow_runs`

func NewPgStore(pool *pgxpool.Pool) *PgStore { return &PgStore{pool: pool} }

func runScanTargets(r *domain.Run, snapshot, input *[]byte) []any {
	return []any{
		&r.ID, &r.DefinitionID, &r.VersionID, &r.VersionNumber, &r.Status, snapshot, input,
		&r.Output, &r.ErrorMessage, &r.IdempotencyKey, &r.RequestHash, &r.Generation,
		&r.SchedulerOwner, &r.LeaseExpiresAt, &r.PauseReason, &r.CancelReason, &r.ManualReason,
		&r.CreatedBy, &r.CreatedAt, &r.UpdatedAt, &r.StartedAt, &r.FinishedAt,
	}
}

func (s *PgStore) exec(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	tc, ok := postgres.FromContext(ctx)
	if ok && tc.TenantID != tenantID {
		return fmt.Errorf("workflow store: tenant context mismatch")
	}
	if !ok {
		ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	}
	return postgres.ExecTenant(ctx, s.pool, fn)
}

func (s *PgStore) CreateDefinition(ctx context.Context, tenantID string, d *domain.Definition) error {
	spec, err := json.Marshal(d.Spec)
	if err != nil {
		return err
	}
	inputSchema, err := json.Marshal(d.InputSchema)
	if err != nil {
		return err
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflow_definitions (id,name,description,draft_revision,draft_spec_json,draft_input_schema_json) VALUES ($1,$2,$3,$4,$5,$6)`, d.ID, d.Name, d.Description, d.Revision, string(spec), string(inputSchema))
		return err
	})
}

func (s *PgStore) GetDefinition(ctx context.Context, tenantID, id string) (*domain.Definition, error) {
	var d domain.Definition
	var raw, rawInputSchema []byte
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,name,description,draft_revision,draft_spec_json,draft_input_schema_json FROM workflow_definitions WHERE id=$1`, id).Scan(&d.ID, &d.Name, &d.Description, &d.Revision, &raw, &rawInputSchema)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &d.Spec); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawInputSchema, &d.InputSchema); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *PgStore) UpdateDefinition(ctx context.Context, tenantID string, d *domain.Definition, expected int64) error {
	spec, err := json.Marshal(d.Spec)
	if err != nil {
		return err
	}
	inputSchema, err := json.Marshal(d.InputSchema)
	if err != nil {
		return err
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_definitions SET name=$1,description=$2,draft_revision=$3,draft_spec_json=$4,draft_input_schema_json=$5,updated_at=NOW() WHERE id=$6 AND draft_revision=$7`, d.Name, d.Description, d.Revision, string(spec), string(inputSchema), d.ID, expected)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrRevisionConflict
		}
		return nil
	})
}

func (s *PgStore) CreateVersion(ctx context.Context, tenantID string, v *domain.Version) error {
	spec, err := json.Marshal(v.Spec)
	if err != nil {
		return err
	}
	inputSchema, err := json.Marshal(v.InputSchema)
	if err != nil {
		return err
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (id,definition_id,version_no,name,description,spec_json,input_schema_json) VALUES ($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.DefinitionID, v.Number, v.Name, v.Description, string(spec), string(inputSchema))
		return err
	})
}

func (s *PgStore) GetVersion(ctx context.Context, tenantID, id string) (*domain.Version, error) {
	var v domain.Version
	var raw, rawInputSchema []byte
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id,definition_id,version_no,name,description,spec_json,input_schema_json FROM workflow_versions WHERE id=$1`, id).Scan(&v.ID, &v.DefinitionID, &v.Number, &v.Name, &v.Description, &raw, &rawInputSchema)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &v.Spec); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawInputSchema, &v.InputSchema); err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *PgStore) NextVersionNumber(ctx context.Context, tenantID, definitionID string) (int64, error) {
	var number int64
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM workflow_versions WHERE definition_id=$1`, definitionID).Scan(&number)
	})
	return number, err
}

func (s *PgStore) CreateNextVersion(ctx context.Context, tenantID string, definition *domain.Definition, versionID string) (*domain.Version, error) {
	if err := domain.ValidateSpec(definition.Spec); err != nil {
		return nil, err
	}
	var created *domain.Version
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM workflow_definitions WHERE id=$1 FOR UPDATE`, definition.ID).Scan(new(string)); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}
		var number int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM workflow_versions WHERE definition_id=$1`, definition.ID).Scan(&number); err != nil {
			return err
		}
		version, err := definition.Publish(versionID, number)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(version.Spec)
		if err != nil {
			return err
		}
		inputSchema, err := json.Marshal(version.InputSchema)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflow_versions (id,definition_id,version_no,name,description,spec_json,input_schema_json) VALUES ($1,$2,$3,$4,$5,$6,$7)`, version.ID, version.DefinitionID, version.Number, version.Name, version.Description, string(raw), string(inputSchema)); err != nil {
			return err
		}
		created = version
		return nil
	})
	return created, err
}

func (s *PgStore) CreateRun(ctx context.Context, tenantID string, r *domain.Run) error {
	snapshot, err := json.Marshal(r.Snapshot)
	if err != nil {
		return err
	}
	input, err := json.Marshal(r.Input)
	if err != nil {
		return err
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflow_runs (id,definition_id,version_id,version_no,status,snapshot_json,input_json,output_text,error_message,idempotency_key,request_hash,generation,scheduler_owner,lease_expires_at,pause_reason,cancel_reason,manual_reason,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, r.ID, r.DefinitionID, r.VersionID, r.VersionNumber, r.Status, string(snapshot), string(input), r.Output, r.ErrorMessage, r.IdempotencyKey, r.RequestHash, r.Generation, r.SchedulerOwner, r.LeaseExpiresAt, r.PauseReason, r.CancelReason, r.ManualReason, r.CreatedBy)
		return err
	})
}

func (s *PgStore) CreateRunIdempotent(ctx context.Context, tenantID string, r *domain.Run) (*domain.Run, bool, error) {
	snapshot, err := json.Marshal(r.Snapshot)
	if err != nil {
		return nil, false, err
	}
	input, err := json.Marshal(r.Input)
	if err != nil {
		return nil, false, err
	}
	var created bool
	var existing domain.Run
	var existingSnapshot, existingInput []byte
	err = s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO workflow_runs (id,definition_id,version_id,version_no,status,snapshot_json,input_json,output_text,error_message,idempotency_key,request_hash,generation,scheduler_owner,lease_expires_at,pause_reason,cancel_reason,manual_reason,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING`, r.ID, r.DefinitionID, r.VersionID, r.VersionNumber, r.Status, string(snapshot), string(input), r.Output, r.ErrorMessage, r.IdempotencyKey, r.RequestHash, r.Generation, r.SchedulerOwner, r.LeaseExpiresAt, r.PauseReason, r.CancelReason, r.ManualReason, r.CreatedBy)
		if err != nil {
			return err
		}
		created = tag.RowsAffected() == 1
		if created {
			existing = *r
			return nil
		}
		if err := tx.QueryRow(ctx, runSelectColumns+` WHERE idempotency_key=$1`, r.IdempotencyKey).Scan(runScanTargets(&existing, &existingSnapshot, &existingInput)...); err != nil {
			return err
		}
		if existing.RequestHash != r.RequestHash {
			return domain.ErrIdempotencyConflict
		}
		return json.Unmarshal(existingSnapshot, &existing.Snapshot)
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		if err := json.Unmarshal(existingInput, &existing.Input); err != nil {
			return nil, false, err
		}
	}
	return &existing, created, nil
}

func (s *PgStore) FindRunByIdempotency(ctx context.Context, tenantID, key string) (*domain.Run, error) {
	return s.getRun(ctx, tenantID, runSelectColumns+` WHERE idempotency_key=$1`, key)
}

func (s *PgStore) GetRun(ctx context.Context, tenantID, id string) (*domain.Run, error) {
	return s.getRun(ctx, tenantID, runSelectColumns+` WHERE id=$1`, id)
}

func (s *PgStore) getRun(ctx context.Context, tenantID, query, arg string) (*domain.Run, error) {
	var r domain.Run
	var snapshot, input []byte
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, arg).Scan(runScanTargets(&r, &snapshot, &input)...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshot, &r.Snapshot); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(input, &r.Input); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PgStore) UpdateRun(ctx context.Context, tenantID string, r *domain.Run) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE workflow_runs SET status=$1,output_text=$2,error_message=$3,generation=$4,pause_reason=$5,cancel_reason=$6,manual_reason=$7,updated_at=NOW(),started_at=CASE WHEN $1='running' THEN COALESCE(started_at,NOW()) ELSE started_at END,finished_at=CASE WHEN $1 IN ('completed','failed','canceled') THEN NOW() ELSE finished_at END WHERE id=$8`, r.Status, r.Output, r.ErrorMessage, r.Generation, r.PauseReason, r.CancelReason, r.ManualReason, r.ID)
		return err
	})
}

func (s *PgStore) ClaimRun(ctx context.Context, owner string, lease time.Duration) (string, *domain.Run, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM public.tenants WHERE status='active' AND deleted_at IS NULL ORDER BY created_at`)
	if err != nil {
		return "", nil, false, err
	}
	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", nil, false, err
		}
		tenantIDs = append(tenantIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", nil, false, err
	}
	rows.Close()
	for _, tenantID := range tenantIDs {
		tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
		var runID string
		err := postgres.ExecTenant(tenantCtx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
			if _, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext(current_schema()))`); lockErr != nil {
				return lockErr
			}
			return tx.QueryRow(ctx, `WITH candidate AS (
				SELECT id FROM workflow_runs
				WHERE (status IN ('queued','pause_requested','cancel_requested') OR (status='running' AND lease_expires_at < NOW()))
				  AND ((status='running' AND lease_expires_at < NOW()) OR (SELECT COUNT(*) FROM workflow_runs WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at > NOW())) < $3)
				ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
			) UPDATE workflow_runs r SET status=CASE WHEN r.status IN ('pause_requested','cancel_requested') THEN r.status ELSE 'running' END,scheduler_owner=$1,lease_expires_at=NOW()+$2::interval,generation=r.generation+1,started_at=COALESCE(r.started_at,NOW()),updated_at=NOW()
			FROM candidate WHERE r.id=candidate.id RETURNING r.id::text`, owner, lease.String(), domain.MaxTenantConcurrentRuns).Scan(&runID)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "42P01" || pgErr.Code == "3F000") {
			continue
		}
		if err != nil {
			return "", nil, false, err
		}
		run, err := s.GetRun(tenantCtx, tenantID, runID)
		return tenantID, run, true, err
	}
	return "", nil, false, nil
}

func (s *PgStore) RenewRunLease(ctx context.Context, tenantID, runID, owner string, generation int64, lease time.Duration) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET lease_expires_at=NOW()+$1::interval,updated_at=NOW() WHERE id=$2 AND status='running' AND scheduler_owner=$3 AND generation=$4`, lease.String(), runID, owner, generation)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrFenceConflict
		}
		return nil
	})
}

func (s *PgStore) RunControlState(ctx context.Context, tenantID, runID string, generation int64) (domain.RunStatus, error) {
	var status domain.RunStatus
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT status FROM workflow_runs WHERE id=$1 AND generation=$2`, runID, generation).Scan(&status)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrGenerationConflict
	}
	return status, err
}

func appendEventTx(ctx context.Context, tx pgx.Tx, event *domain.Event) error {
	if event.ID == "" {
		return fmt.Errorf("workflow event id is required")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	if err := tx.QueryRow(ctx, `UPDATE workflow_runs SET next_event_sequence=next_event_sequence+1 WHERE id=$1 RETURNING next_event_sequence-1`, event.RunID).Scan(&event.SequenceNo); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workflow_events (id,run_id,sequence_no,event_type,node_id,attempt_no,status,actor_type,actor_id,summary,payload_json,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, event.ID, event.RunID, event.SequenceNo, event.Type, event.NodeID, event.AttemptNo, event.Status, event.ActorType, event.ActorID, event.Summary, string(payload), event.OccurredAt)
	return err
}

func (s *PgStore) ControlRun(ctx context.Context, tenantID, runID string, expectedGeneration int64, status domain.RunStatus, reason string, event domain.Event) error {
	event.RunID, event.Status = runID, string(status)
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET status=$1,generation=generation+1,pause_reason=CASE WHEN $1 IN ('pause_requested','paused') THEN $2 ELSE pause_reason END,cancel_reason=CASE WHEN $1='cancel_requested' THEN $2 ELSE cancel_reason END,manual_reason=CASE WHEN $1='manual_intervention' THEN $2 ELSE manual_reason END,scheduler_owner='',lease_expires_at=NULL,updated_at=NOW() WHERE id=$3 AND generation=$4 AND CASE $1 WHEN 'pause_requested' THEN status IN ('queued','running') WHEN 'cancel_requested' THEN status IN ('queued','running','pause_requested','paused','manual_intervention') WHEN 'queued' THEN status IN ('paused','manual_intervention') WHEN 'paused' THEN status='pause_requested' WHEN 'canceled' THEN status='cancel_requested' WHEN 'manual_intervention' THEN status='running' ELSE FALSE END`, status, reason, runID, expectedGeneration)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			var currentGeneration int64
			lookup := tx.QueryRow(ctx, `SELECT generation FROM workflow_runs WHERE id=$1`, runID).Scan(&currentGeneration)
			if errors.Is(lookup, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			if lookup != nil {
				return lookup
			}
			if currentGeneration != expectedGeneration {
				return domain.ErrGenerationConflict
			}
			return domain.ErrInvalidTransition
		}
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) CreateApproval(ctx context.Context, tenantID string, approval *domain.Approval, event domain.Event) error {
	event.RunID, event.NodeID = approval.RunID, approval.NodeID
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO workflow_approvals (id,run_id,node_id,attempt_id,run_generation,reason,risk,request_summary,status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, approval.ID, approval.RunID, approval.NodeID, approval.AttemptID, approval.RunGeneration, approval.Reason, approval.Risk, approval.RequestSummary, approval.Status)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET status='paused',generation=$1,pause_reason=$2,scheduler_owner='',lease_expires_at=NULL,updated_at=NOW() WHERE id=$3 AND generation=$4 AND status IN ('running','pause_requested')`, approval.RunGeneration, approval.Reason, approval.RunID, approval.RunGeneration-1)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrGenerationConflict
		}
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) GetApproval(ctx context.Context, tenantID, id string) (*domain.Approval, error) {
	var a domain.Approval
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id::text,run_id::text,node_id,attempt_id::text,run_generation,reason,risk,request_summary,status,decision_actor,decision_comment,decided_at FROM workflow_approvals WHERE id=$1`, id).Scan(&a.ID, &a.RunID, &a.NodeID, &a.AttemptID, &a.RunGeneration, &a.Reason, &a.Risk, &a.RequestSummary, &a.Status, &a.DecisionActor, &a.DecisionComment, &a.DecidedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &a, err
}

func (s *PgStore) ListApprovals(ctx context.Context, tenantID, runID string, pendingOnly bool) ([]domain.Approval, error) {
	query := `SELECT id::text,run_id::text,node_id,attempt_id::text,run_generation,reason,risk,request_summary,status,decision_actor,decision_comment,decided_at FROM workflow_approvals WHERE ($1='' OR run_id=$1::uuid) AND (NOT $2 OR status='pending') ORDER BY created_at`
	var out []domain.Approval
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, runID, pendingOnly)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.Approval
			if err := rows.Scan(&a.ID, &a.RunID, &a.NodeID, &a.AttemptID, &a.RunGeneration, &a.Reason, &a.Risk, &a.RequestSummary, &a.Status, &a.DecisionActor, &a.DecisionComment, &a.DecidedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) DecideApproval(ctx context.Context, tenantID, id string, generation int64, attemptID string, decision domain.ApprovalDecision, actor, comment string, event domain.Event) error {
	var status domain.ApprovalStatus
	switch decision {
	case domain.ApprovalDecisionApprove:
		status = domain.ApprovalStatusApproved
	case domain.ApprovalDecisionReject:
		status = domain.ApprovalStatusRejected
	default:
		return fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalidSpec)
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var runID string
		err := tx.QueryRow(ctx, `UPDATE workflow_approvals SET status=$1,decision_actor=$2,decision_comment=$3,decided_at=NOW() WHERE id=$4 AND run_generation=$5 AND attempt_id=$6 AND status='pending' RETURNING run_id::text`, status, actor, comment, id, generation, attemptID).Scan(&runID)
		if errors.Is(err, pgx.ErrNoRows) {
			var current domain.ApprovalStatus
			lookup := tx.QueryRow(ctx, `SELECT status FROM workflow_approvals WHERE id=$1`, id).Scan(&current)
			if errors.Is(lookup, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			if lookup != nil {
				return lookup
			}
			if current != domain.ApprovalStatusPending {
				return domain.ErrDecisionConflict
			}
			return domain.ErrGenerationConflict
		}
		if err != nil {
			return err
		}
		event.RunID, event.Status = runID, string(status)
		if decision == domain.ApprovalDecisionReject {
			tag, updateErr := tx.Exec(ctx, `UPDATE workflow_runs SET status='failed',error_message=$1,generation=generation+1,scheduler_owner='',lease_expires_at=NULL,finished_at=NOW(),updated_at=NOW() WHERE id=$2 AND generation=$3 AND status='paused'`, "approval rejected: "+comment, runID, generation)
			if updateErr != nil {
				return updateErr
			}
			if tag.RowsAffected() != 1 {
				return domain.ErrGenerationConflict
			}
			event.Status = string(domain.RunStatusFailed)
		}
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) CreateEffectIntent(ctx context.Context, tenantID string, i *domain.EffectIntent) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO workflow_effect_intents (id,run_id,node_id,attempt_id,run_generation,effect_class,idempotency_key,status,reason,output_summary) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (idempotency_key) DO UPDATE SET attempt_id=EXCLUDED.attempt_id,run_generation=EXCLUDED.run_generation,reason='',output_summary='',updated_at=NOW() WHERE workflow_effect_intents.status='prepared' RETURNING id::text`, i.ID, i.RunID, i.NodeID, i.AttemptID, i.RunGeneration, i.EffectClass, i.IdempotencyKey, i.Status, i.Reason, i.OutputSummary).Scan(&i.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrFenceConflict
		}
		return err
	})
}

func (s *PgStore) StartExternalEffect(ctx context.Context, tenantID string, i *domain.EffectIntent, owner string, generation int64) error {
	if owner == "" {
		return domain.ErrFenceConflict
	}
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT TRUE FROM workflow_runs r JOIN workflow_node_attempts a ON a.run_id=r.id
			WHERE r.id=$1 AND r.generation=$2 AND r.scheduler_owner=$3 AND r.status='running'
			  AND r.lease_expires_at>NOW() AND a.id=$4 AND a.fence_token=$2
			  AND a.run_generation=$2 AND a.status='running'
			FOR UPDATE OF r,a`, i.RunID, generation, owner, i.AttemptID).Scan(&allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrFenceConflict
		}
		if err != nil {
			return err
		}
		i.RunGeneration, i.Status = generation, domain.EffectIntentStatusStarted
		err = tx.QueryRow(ctx, `INSERT INTO workflow_effect_intents (id,run_id,node_id,attempt_id,run_generation,effect_class,idempotency_key,status,reason,output_summary)
			VALUES ($1,$2,$3,$4,$5,$6,$7,'started','','')
			ON CONFLICT (idempotency_key) DO UPDATE SET attempt_id=EXCLUDED.attempt_id,run_generation=EXCLUDED.run_generation,status='started',reason='',output_summary='',updated_at=NOW()
			WHERE workflow_effect_intents.status='prepared' RETURNING id::text`, i.ID, i.RunID, i.NodeID, i.AttemptID, generation, i.EffectClass, i.IdempotencyKey).Scan(&i.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrFenceConflict
		}
		return err
	})
}
func (s *PgStore) UpdateEffectIntent(ctx context.Context, tenantID string, i *domain.EffectIntent, expected domain.EffectIntentStatus) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_effect_intents SET status=$1,reason=$2,output_summary=$3,updated_at=NOW() WHERE id=$4 AND run_generation=$5 AND status=$6`, i.Status, i.Reason, i.OutputSummary, i.ID, i.RunGeneration, expected)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrFenceConflict
		}
		return nil
	})
}
func (s *PgStore) ListEffectIntents(ctx context.Context, tenantID, runID string) ([]domain.EffectIntent, error) {
	var out []domain.EffectIntent
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text,run_id::text,node_id,attempt_id::text,run_generation,effect_class,idempotency_key,status,reason,output_summary FROM workflow_effect_intents WHERE run_id=$1 ORDER BY created_at`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var i domain.EffectIntent
			if err := rows.Scan(&i.ID, &i.RunID, &i.NodeID, &i.AttemptID, &i.RunGeneration, &i.EffectClass, &i.IdempotencyKey, &i.Status, &i.Reason, &i.OutputSummary); err != nil {
				return err
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) ResolveEffect(ctx context.Context, tenantID, id string, generation int64, action domain.ManualAction, output, _ string, event domain.Event) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var runID, attemptID string
		var class domain.EffectClass
		var status domain.EffectIntentStatus
		var executionGeneration int64
		err := tx.QueryRow(ctx, `SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=$1 FOR UPDATE`, id).Scan(&runID, &attemptID, &class, &status, &executionGeneration)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrGenerationConflict
		}
		if err != nil {
			return err
		}
		if status != domain.EffectIntentStatusUnknown {
			return domain.ErrInvalidTransition
		}
		var intentStatus domain.EffectIntentStatus
		var runStatus domain.RunStatus
		var attemptStatus domain.AttemptStatus
		switch action {
		case domain.ManualActionMarkSucceeded:
			intentStatus, runStatus, attemptStatus = domain.EffectIntentStatusSucceeded, domain.RunStatusQueued, domain.AttemptStatusSucceeded
		case domain.ManualActionRetry:
			intentStatus, runStatus, attemptStatus = domain.EffectIntentStatusPrepared, domain.RunStatusQueued, domain.AttemptStatusRetryWait
		case domain.ManualActionTerminate:
			intentStatus, runStatus, attemptStatus = domain.EffectIntentStatusFailed, domain.RunStatusFailed, domain.AttemptStatusFailed
		default:
			return domain.ErrInvalidTransition
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_effect_intents SET status=$1,output_summary=$2,reason='',updated_at=NOW() WHERE id=$3 AND status='unknown'`, intentStatus, output, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE workflow_node_attempts SET status=$1,output_summary=$2,updated_at=NOW(),finished_at=CASE WHEN $1 IN ('succeeded','failed') THEN NOW() ELSE finished_at END WHERE id=$3 AND run_generation=$4`, attemptStatus, output, attemptID, executionGeneration); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET status=$1,generation=generation+1,manual_reason='',scheduler_owner='',lease_expires_at=NULL,updated_at=NOW(),finished_at=CASE WHEN $1='failed' THEN NOW() ELSE NULL END WHERE id=$2 AND generation=$3`, runStatus, runID, generation)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrGenerationConflict
		}
		event.RunID, event.Status = runID, string(runStatus)
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) ReleaseRun(ctx context.Context, tenantID, runID, owner string, generation int64) error {
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return postgres.ExecTenant(tenantCtx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET scheduler_owner='',lease_expires_at=NULL,updated_at=NOW() WHERE id=$1 AND scheduler_owner=$2 AND generation=$3`, runID, owner, generation)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrFenceConflict
		}
		return nil
	})
}

func (s *PgStore) SaveAttempt(ctx context.Context, tenantID string, a domain.NodeAttempt) error {
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error { return saveAttemptTx(ctx, tx, a) })
}

func saveAttemptTx(ctx context.Context, tx pgx.Tx, a domain.NodeAttempt) error {
	selectedEdges, err := json.Marshal(a.SelectedEdges)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO workflow_node_attempts (id,run_id,node_id,attempt_no,status,input_text,output_summary,error_message,error_code,trace_id,fence_token,run_generation,retry_at,effect_class,selected_edges_json,started_at,finished_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,CASE WHEN $5='running' THEN NOW() END,CASE WHEN $5 IN ('succeeded','failed','skipped','canceled','manual_intervention') THEN NOW() END) ON CONFLICT (run_id,node_id,attempt_no) DO UPDATE SET status=EXCLUDED.status,output_summary=EXCLUDED.output_summary,error_message=EXCLUDED.error_message,error_code=EXCLUDED.error_code,trace_id=EXCLUDED.trace_id,fence_token=EXCLUDED.fence_token,run_generation=EXCLUDED.run_generation,retry_at=EXCLUDED.retry_at,effect_class=EXCLUDED.effect_class,selected_edges_json=EXCLUDED.selected_edges_json,updated_at=NOW(),finished_at=CASE WHEN EXCLUDED.status IN ('succeeded','failed','skipped','canceled','manual_intervention') THEN NOW() ELSE workflow_node_attempts.finished_at END WHERE workflow_node_attempts.fence_token < EXCLUDED.fence_token OR (workflow_node_attempts.fence_token = EXCLUDED.fence_token AND workflow_node_attempts.status NOT IN ('succeeded','failed','skipped','canceled','manual_intervention'))`, a.ID, a.RunID, a.NodeID, a.AttemptNo, a.Status, a.Input, a.OutputSummary, a.ErrorMessage, a.ErrorCode, a.TraceID, a.FenceToken, a.RunGeneration, a.RetryAt, a.EffectClass, string(selectedEdges))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrFenceConflict
	}
	return nil
}

func (s *PgStore) CheckpointAttempt(ctx context.Context, tenantID string, attempt domain.NodeAttempt, event domain.Event) error {
	event.RunID = attempt.RunID
	event.NodeID = attempt.NodeID
	event.AttemptNo = attempt.AttemptNo
	event.Status = string(attempt.Status)
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := saveAttemptTx(ctx, tx, attempt); err != nil {
			return err
		}
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) CheckpointRun(ctx context.Context, tenantID string, run *domain.Run, event domain.Event) error {
	event.RunID, event.Status = run.ID, string(run.Status)
	return s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE workflow_runs SET status=$1,output_text=$2,error_message=$3,pause_reason=$4,cancel_reason=$5,manual_reason=$6,updated_at=NOW(),finished_at=CASE WHEN $1 IN ('completed','failed','canceled') THEN NOW() ELSE finished_at END WHERE id=$7 AND generation=$8`, run.Status, run.Output, run.ErrorMessage, run.PauseReason, run.CancelReason, run.ManualReason, run.ID, run.Generation)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrGenerationConflict
		}
		return appendEventTx(ctx, tx, &event)
	})
}

func (s *PgStore) ListAttempts(ctx context.Context, tenantID, runID string) ([]domain.NodeAttempt, error) {
	var out []domain.NodeAttempt
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,run_id,node_id,attempt_no,status,input_text,output_summary,error_message,error_code,trace_id,fence_token,run_generation,retry_at,effect_class,selected_edges_json FROM workflow_node_attempts WHERE run_id=$1 ORDER BY created_at,node_id,attempt_no`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a domain.NodeAttempt
			var selected []byte
			if err := rows.Scan(&a.ID, &a.RunID, &a.NodeID, &a.AttemptNo, &a.Status, &a.Input, &a.OutputSummary, &a.ErrorMessage, &a.ErrorCode, &a.TraceID, &a.FenceToken, &a.RunGeneration, &a.RetryAt, &a.EffectClass, &selected); err != nil {
				return err
			}
			if err := json.Unmarshal(selected, &a.SelectedEdges); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *PgStore) AppendEvent(ctx context.Context, tenantID string, event domain.Event) (domain.Event, error) {
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return appendEventTx(ctx, tx, &event)
	})
	return event, err
}

func (s *PgStore) ListEvents(ctx context.Context, tenantID, runID string, after int64, limit int) ([]domain.Event, error) {
	if _, err := s.GetRun(ctx, tenantID, runID); err != nil {
		return nil, err
	}
	var events []domain.Event
	err := s.exec(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id::text,run_id::text,sequence_no,event_type,status,node_id,attempt_no,actor_type,actor_id,summary,payload_json,created_at FROM workflow_events WHERE run_id=$1 AND sequence_no>$2 ORDER BY sequence_no LIMIT $3`, runID, after, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event domain.Event
			var payload []byte
			if err := rows.Scan(&event.ID, &event.RunID, &event.SequenceNo, &event.Type, &event.Status, &event.NodeID, &event.AttemptNo, &event.ActorType, &event.ActorID, &event.Summary, &payload, &event.OccurredAt); err != nil {
				return err
			}
			if err := json.Unmarshal(payload, &event.Payload); err != nil {
				return err
			}
			events = append(events, event)
		}
		return rows.Err()
	})
	return events, err
}
