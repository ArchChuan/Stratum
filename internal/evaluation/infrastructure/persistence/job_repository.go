package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgJobRepository struct {
	pool *pgxpool.Pool
}

func NewPgJobRepository(pool *pgxpool.Pool) *PgJobRepository {
	return &PgJobRepository{pool: pool}
}

func (r *PgJobRepository) Enqueue(
	ctx context.Context,
	tenantID string,
	job domain.EvaluationJob,
) (domain.EvaluationJob, error) {
	payload, err := json.Marshal(job.Payload)
	if err != nil {
		return domain.EvaluationJob{}, fmt.Errorf("evaluation job repository: marshal payload: %w", err)
	}
	var saved domain.EvaluationJob
	err = r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var payloadJSON []byte
		var status string
		if err := tx.QueryRow(ctx,
			`INSERT INTO evaluation_jobs (id, job_type, payload, status, idempotency_key, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6)
			 ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
			 RETURNING id, job_type, payload, status, attempts, idempotency_key, error_message, result_id, created_at`,
			job.ID, job.Type, string(payload), string(job.Status), job.IdempotencyKey, job.CreatedAt,
		).Scan(&saved.ID, &saved.Type, &payloadJSON, &status, &saved.Attempts,
			&saved.IdempotencyKey, &saved.ErrorMessage, &saved.ResultID, &saved.CreatedAt); err != nil {
			return err
		}
		if err := json.Unmarshal(payloadJSON, &saved.Payload); err != nil {
			return fmt.Errorf("evaluation job repository: unmarshal payload: %w", err)
		}
		saved.Status = domain.JobStatus(status)
		return nil
	})
	return saved, err
}

func (r *PgJobRepository) Get(
	ctx context.Context,
	tenantID, jobID string,
) (domain.EvaluationJob, bool, error) {
	var job domain.EvaluationJob
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var payloadJSON []byte
		var status string
		err := tx.QueryRow(ctx,
			`SELECT id, job_type, payload, status, attempts, idempotency_key, error_message, result_id, created_at
			 FROM evaluation_jobs WHERE id=$1`, jobID,
		).Scan(&job.ID, &job.Type, &payloadJSON, &status, &job.Attempts,
			&job.IdempotencyKey, &job.ErrorMessage, &job.ResultID, &job.CreatedAt)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		job.Status = domain.JobStatus(status)
		return json.Unmarshal(payloadJSON, &job.Payload)
	})
	return job, found, err
}

func (r *PgJobRepository) Claim(
	ctx context.Context,
	tenantID, workerID string,
	lease time.Duration,
) (*domain.EvaluationJob, error) {
	var claimed *domain.EvaluationJob
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var job domain.EvaluationJob
		var payloadJSON []byte
		var status string
		err := tx.QueryRow(ctx,
			`SELECT id, job_type, payload, status, attempts, idempotency_key, error_message, result_id, created_at
			 FROM evaluation_jobs
			 WHERE status='queued' OR (status='running' AND lease_until < NOW())
			 ORDER BY created_at
			 FOR UPDATE SKIP LOCKED LIMIT 1`,
		).Scan(&job.ID, &job.Type, &payloadJSON, &status, &job.Attempts,
			&job.IdempotencyKey, &job.ErrorMessage, &job.ResultID, &job.CreatedAt)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE evaluation_jobs
			 SET status='running', attempts=attempts+1, lease_owner=$2,
			     lease_until=NOW()+make_interval(secs => $3), updated_at=NOW()
			 WHERE id=$1`, job.ID, workerID, lease.Seconds()); err != nil {
			return err
		}
		if err := json.Unmarshal(payloadJSON, &job.Payload); err != nil {
			return err
		}
		job.Status = domain.JobRunning
		job.Attempts++
		claimed = &job
		return nil
	})
	return claimed, err
}

func (r *PgJobRepository) Complete(ctx context.Context, tenantID, jobID, resultID string) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE evaluation_jobs
			 SET status=$2, result_id=$3, error_message='', lease_owner='', lease_until=NULL, updated_at=NOW()
			 WHERE id=$1`, jobID, string(domain.JobSucceeded), resultID)
		return err
	})
}

func (r *PgJobRepository) Fail(ctx context.Context, tenantID, jobID, errorMessage string) error {
	return r.setTerminal(ctx, tenantID, jobID, domain.JobFailed, errorMessage)
}

func (r *PgJobRepository) setTerminal(
	ctx context.Context,
	tenantID, jobID string,
	status domain.JobStatus,
	errorMessage string,
) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE evaluation_jobs
			 SET status=$2, error_message=$3, lease_owner='', lease_until=NULL, updated_at=NOW()
			 WHERE id=$1`, jobID, string(status), errorMessage)
		return err
	})
}

func (r *PgJobRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}
