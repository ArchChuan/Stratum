package wiring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	schedapp "github.com/byteBuilderX/stratum/internal/scheduler/application"
	schedport "github.com/byteBuilderX/stratum/internal/scheduler/domain/port"
	schedpersist "github.com/byteBuilderX/stratum/internal/scheduler/infrastructure/persistence"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Scheduler groups the scheduled-task service and its fire worker.
type Scheduler struct {
	Service *schedapp.Service
	Worker  *schedapp.Worker
}

// schedulerWorkflowRunner starts queued runs via the workflow RunService.
// Deterministic failures (version missing, input rejected, idempotency
// conflict) are wrapped with port.ErrDeterministicFailure so the fire loop
// advances the schedule and records the error; anything else stays transient
// and is retried on the next poll.
type schedulerWorkflowRunner struct{ runs *workflowapp.RunService }

func (r schedulerWorkflowRunner) StartAsync(ctx context.Context, tenantID, versionID string, input map[string]any, idempotencyKey, createdBy string) error {
	_, _, err := r.runs.StartAsync(ctx, tenantID, workflowapp.StartRunCommand{
		VersionID:      versionID,
		Input:          input,
		IdempotencyKey: idempotencyKey,
		CreatedBy:      createdBy,
	})
	if err != nil && isDeterministicFireError(err) {
		return fmt.Errorf("%w: %v", schedport.ErrDeterministicFailure, err)
	}
	return err
}

// isDeterministicFireError reports whether retrying the fire cannot fix the
// error. ErrNotFound (version deleted) and InputValidationError (template no
// longer matches the schema) are permanent; ErrIdempotencyConflict means the
// key collided with different input, which the scheduler never retries.
func isDeterministicFireError(err error) bool {
	var inputErr *workflowdomain.InputValidationError
	return errors.Is(err, workflowdomain.ErrNotFound) ||
		errors.As(err, &inputErr) ||
		errors.Is(err, workflowdomain.ErrIdempotencyConflict)
}

// schedulerVersionResolver validates workflow-version references at
// create/update time: ownership of the version by the workflow plus input
// template conformance to the version's declared schema.
type schedulerVersionResolver struct {
	definitions *workflowapp.DefinitionService
}

func (r schedulerVersionResolver) GetVersion(ctx context.Context, tenantID, versionID string) (*schedport.VersionInfo, error) {
	version, err := r.definitions.GetVersion(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	return &schedport.VersionInfo{DefinitionID: version.DefinitionID}, nil
}

func (r schedulerVersionResolver) ValidateInput(ctx context.Context, tenantID, versionID string, input map[string]any) error {
	version, err := r.definitions.GetVersion(ctx, tenantID, versionID)
	if err != nil {
		return err
	}
	return workflowdomain.ValidateRunInput(version.InputSchema, input)
}

// schedulerTenantLister yields only active tenants: firing scheduled runs
// consumes user workflows, so a disabled/suspended tenant must never trigger.
type schedulerTenantLister struct{ pool *pgxpool.Pool }

func (l schedulerTenantLister) ListTenantIDs(ctx context.Context) ([]string, error) {
	rows, err := l.pool.Query(ctx,
		`SELECT id FROM tenants WHERE deleted_at IS NULL AND status='active'`)
	if err != nil {
		return nil, fmt.Errorf("scheduler list tenants: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scheduler scan tenant: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler tenants iteration: %w", err)
	}
	return ids, nil
}

// buildScheduler wires the scheduled-task service and fire worker. It needs
// the workflow services as its runner and version resolver, so this step
// runs after workflow.
func (c *Container) buildScheduler(_ context.Context) error {
	db := c.dbOrNil()
	if db == nil || c.Workflow == nil || c.Workflow.RunService == nil || c.Workflow.DefinitionService == nil {
		return nil
	}
	repo := schedpersist.NewPgScheduledTaskRepo(db)
	newID := func() string { return uuid.Must(uuid.NewV7()).String() }
	service := schedapp.NewService(repo,
		schedulerWorkflowRunner{runs: c.Workflow.RunService},
		schedulerVersionResolver{definitions: c.Workflow.DefinitionService},
		c.platformMetrics(), c.Logger, newID, time.Now)
	worker := schedapp.NewWorker(schedulerTenantLister{pool: db}, service,
		constants.SchedulerPollInterval, c.platformMetrics())
	c.Scheduler = &Scheduler{Service: service, Worker: worker}
	return nil
}
