package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/scheduler/domain"
	"github.com/byteBuilderX/stratum/internal/scheduler/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// Actor identifies the caller of a scheduled-task operation.
type Actor struct {
	UserID string
	Role   string
}

// CreateCommand is the admin input for creating a scheduled task.
type CreateCommand struct {
	Name          string
	WorkflowID    string
	VersionID     string
	InputTemplate map[string]any
	CronExpr      string
}

// UpdateCommand replaces every editable field of a scheduled task.
type UpdateCommand struct {
	Name          string
	WorkflowID    string
	VersionID     string
	InputTemplate map[string]any
	CronExpr      string
}

// Service orchestrates scheduled-task CRUD and the due-fire loop.
type Service struct {
	repo    port.Repository
	runner  port.WorkflowRunner
	version port.WorkflowVersionResolver
	metrics observability.MetricsProvider
	logger  *zap.Logger
	newID   func() string
	now     func() time.Time
}

// NewService constructs the scheduler service with its injected ports.
func NewService(repo port.Repository, runner port.WorkflowRunner, version port.WorkflowVersionResolver, metrics observability.MetricsProvider, logger *zap.Logger, newID func() string, now func() time.Time) *Service {
	return &Service{repo: repo, runner: runner, version: version, metrics: metrics, logger: logger, newID: newID, now: now}
}

// Create validates the cron expression and workflow-version reference, then
// persists a new enabled task scheduled at its first fire time.
func (s *Service) Create(ctx context.Context, tenantID string, cmd CreateCommand, actor Actor) (*domain.ScheduledTask, error) {
	if err := authorizeControl(actor); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	next, err := s.validateAndSchedule(ctx, tenantID, cmd.WorkflowID, cmd.VersionID, cmd.InputTemplate, cmd.CronExpr, now)
	if err != nil {
		return nil, err
	}
	task, err := domain.NewScheduledTask(s.newID(), cmd.Name, cmd.WorkflowID, cmd.VersionID, cmd.InputTemplate, cmd.CronExpr, next)
	if err != nil {
		return nil, err
	}
	task.CreatedBy = actor.UserID
	task.CreatedAt, task.UpdatedAt = now, now
	if err := s.repo.Insert(ctx, tenantID, task); err != nil {
		return nil, fmt.Errorf("scheduler create: %w", err)
	}
	return task, nil
}

// Update replaces every editable field; the workflow-version reference and
// input template are re-validated against the target version's schema.
func (s *Service) Update(ctx context.Context, tenantID, id string, cmd UpdateCommand, actor Actor) (*domain.ScheduledTask, error) {
	existing, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := authorizeControl(actor); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	next, err := s.validateAndSchedule(ctx, tenantID, cmd.WorkflowID, cmd.VersionID, cmd.InputTemplate, cmd.CronExpr, now)
	if err != nil {
		return nil, err
	}
	task, err := domain.NewScheduledTask(existing.ID, cmd.Name, cmd.WorkflowID, cmd.VersionID, cmd.InputTemplate, cmd.CronExpr, next)
	if err != nil {
		return nil, err
	}
	// Preserve ownership and run history; an update re-enables the task so a
	// previously disabled schedule resumes from the freshly computed fire time.
	task.CreatedBy = existing.CreatedBy
	task.CreatedAt = existing.CreatedAt
	task.LastRunAt = existing.LastRunAt
	task.LastRunStatus = existing.LastRunStatus
	task.LastErrorMessage = existing.LastErrorMessage
	task.UpdatedAt = now
	if err := s.repo.Update(ctx, tenantID, task); err != nil {
		return nil, fmt.Errorf("scheduler update: %w", err)
	}
	return task, nil
}

// Delete removes a scheduled task entirely.
func (s *Service) Delete(ctx context.Context, tenantID, id string, actor Actor) error {
	if _, err := s.repo.GetByID(ctx, tenantID, id); err != nil {
		return err
	}
	if err := authorizeControl(actor); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, tenantID, id); err != nil {
		return fmt.Errorf("scheduler delete: %w", err)
	}
	return nil
}

// SetEnabled flips the enabled flag; re-enabling recomputes next_fire_at so
// a long-disabled task never fires immediately or forever.
func (s *Service) SetEnabled(ctx context.Context, tenantID, id string, enabled bool, actor Actor) error {
	st, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := authorizeControl(actor); err != nil {
		return err
	}
	var next *time.Time
	if enabled {
		n, err := nextFireTime(st.CronExpr, s.now().UTC())
		if err != nil {
			return err
		}
		next = &n
	}
	if err := s.repo.SetEnabled(ctx, tenantID, id, enabled, next); err != nil {
		return fmt.Errorf("scheduler set enabled: %w", err)
	}
	return nil
}

// Get returns one task; ErrScheduledTaskNotFound when absent.
func (s *Service) Get(ctx context.Context, tenantID, id string) (*domain.ScheduledTask, error) {
	task, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := s.fillNames(ctx, tenantID, []*domain.ScheduledTask{task}); err != nil {
		return nil, err
	}
	return task, nil
}

// List returns a page of tasks newest-first with the total count.
func (s *Service) List(ctx context.Context, tenantID string, page, pageSize int) ([]domain.ScheduledTask, int, error) {
	limit := pageSize
	if limit <= 0 || limit > constants.MaxPageSize {
		limit = constants.DefaultPageSize
	}
	offset := 0
	if page > 0 {
		offset = (page - 1) * limit
	}
	tasks, total, err := s.repo.List(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	pointers := make([]*domain.ScheduledTask, len(tasks))
	for i := range tasks {
		pointers[i] = &tasks[i]
	}
	if err := s.fillNames(ctx, tenantID, pointers); err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

// fillNames resolves the display names (workflow name + version number/name)
// onto each task. Deleted versions simply keep their raw IDs: a removed
// workflow version must not make the scheduled-task list/detail fail.
func (s *Service) fillNames(ctx context.Context, tenantID string, tasks []*domain.ScheduledTask) error {
	versionIDs := make([]string, 0, len(tasks))
	seen := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		if t == nil || t.VersionID == "" {
			continue
		}
		if _, ok := seen[t.VersionID]; ok {
			continue
		}
		seen[t.VersionID] = struct{}{}
		versionIDs = append(versionIDs, t.VersionID)
	}
	if len(versionIDs) == 0 {
		return nil
	}
	names, err := s.version.ResolveVersionNames(ctx, tenantID, versionIDs)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		info, ok := names[t.VersionID]
		if !ok {
			continue
		}
		t.WorkflowName = info.WorkflowName
		t.VersionNo = info.VersionNo
		t.VersionName = info.VersionName
	}
	return nil
}

// PollTenant fires every due task of one tenant, oldest due first. Failures
// are joined so one bad task does not hide the others.
func (s *Service) PollTenant(ctx context.Context, tenantID string, now time.Time) error {
	tasks, err := s.repo.ListDue(ctx, tenantID, now, constants.MaxSchedulerDueBatchSize)
	if err != nil {
		return fmt.Errorf("scheduler list due: %w", err)
	}
	var failures []error
	for i := range tasks {
		if err := s.fireOne(ctx, tenantID, &tasks[i], now); err != nil {
			failures = append(failures, fmt.Errorf("scheduler fire %s: %w", tasks[i].ID, err))
		}
	}
	return errors.Join(failures...)
}

// fireOne starts the queued run for one due task and advances the schedule.
// Transient failures do not advance next_fire_at (retried next poll; the
// idempotency key prevents a duplicate run). Deterministic failures (missing
// version, invalid input, broken cron) advance with an error record so the
// task does not spin forever.
func (s *Service) fireOne(ctx context.Context, tenantID string, st *domain.ScheduledTask, now time.Time) error {
	next, cronErr := nextFireTime(st.CronExpr, now)
	if cronErr != nil {
		// Broken cron cannot produce a next time; cool down for a day so the
		// task leaves the due set instead of erroring every poll, and the
		// admin sees last_error_message pointing at the cron expression.
		return s.recordFire(ctx, tenantID, st, now, domain.LastRunError, cronErr.Error(), now.Add(constants.SchedulerCronBrokenCooldown))
	}
	key := fireIDKey(st.ID, st.NextFireAt)
	err := s.runner.StartAsync(ctx, tenantID, st.VersionID, st.InputTemplate, key, schedulerCreatedByPrefix+st.ID)
	if err != nil {
		s.metrics.IncScheduledFire(scheduleTypeCron, "error")
		if errors.Is(err, port.ErrDeterministicFailure) {
			s.logger.Error("scheduled task fire rejected (advancing schedule)", zap.String("schedule_id", st.ID), zap.String("workflow_id", st.WorkflowID), zap.Error(err))
			return s.recordFire(ctx, tenantID, st, now, domain.LastRunError, err.Error(), next)
		}
		s.logger.Error("scheduled task fire failed transiently (will retry next poll)", zap.String("schedule_id", st.ID), zap.String("workflow_id", st.WorkflowID), zap.Error(err))
		return err
	}
	s.metrics.IncScheduledFire(scheduleTypeCron, "ok")
	return s.recordFire(ctx, tenantID, st, now, domain.LastRunOK, "", next)
}

// recordFire advances the schedule guarded on the row's current next_fire_at;
// a concurrent worker's advance is a silent no-op (RowsAffected == 0).
func (s *Service) recordFire(ctx context.Context, tenantID string, st *domain.ScheduledTask, firedAt time.Time, status, errorMsg string, newNext time.Time) error {
	advanced, err := s.repo.RecordFire(ctx, tenantID, st.ID, firedAt, status, errorMsg, st.NextFireAt, newNext)
	if err != nil {
		return fmt.Errorf("scheduler record fire: %w", err)
	}
	if !advanced {
		s.logger.Debug("scheduled task fire skipped (concurrent worker advanced)", zap.String("schedule_id", st.ID))
	}
	return nil
}

// fireIDKey builds the workflow-run idempotency key. nextFireAt MUST be the
// row's current value read by ListDue — not a freshly computed time — so all
// worker instances agree on the key for the same scheduled fire.
func fireIDKey(scheduleID string, nextFireAt time.Time) string {
	return fmt.Sprintf(idempotencyKeyFormat, scheduleID, nextFireAt.UTC().Format(time.RFC3339))
}

// validateAndSchedule computes the next fire time and verifies the
// workflow-version reference: the version must belong to workflowID and the
// input template must satisfy the version's declared input schema.
func (s *Service) validateAndSchedule(ctx context.Context, tenantID, workflowID, versionID string, input map[string]any, cronExpr string, now time.Time) (time.Time, error) {
	next, err := nextFireTime(cronExpr, now)
	if err != nil {
		return time.Time{}, err
	}
	info, err := s.version.GetVersion(ctx, tenantID, versionID)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler resolve version: %w", err)
	}
	if info.DefinitionID != workflowID {
		return time.Time{}, fmt.Errorf("%w: version does not belong to the workflow", domain.ErrScheduledTaskInvalidInput)
	}
	if err := s.version.ValidateInput(ctx, tenantID, versionID, input); err != nil {
		return time.Time{}, fmt.Errorf("%w: input template does not match the workflow version schema", domain.ErrScheduledTaskInvalidInput)
	}
	return next, nil
}

// authorizeControl gates control actions: only tenant admins/owners may
// create, edit, delete or toggle scheduled tasks.
func authorizeControl(actor Actor) error {
	if actor.UserID == "" {
		return domain.ErrScheduledTaskForbidden
	}
	if actor.Role != "admin" && actor.Role != "owner" {
		return domain.ErrScheduledTaskForbidden
	}
	return nil
}

// parseCron parses a standard cron expression, pins the timezone to UTC
// (robfig defaults to time.Local) and rejects schedules that fire more
// often than once per minute.
func parseCron(expr string, now time.Time) (cron.Schedule, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrScheduledTaskInvalidCron, err)
	}
	if spec, ok := sched.(*cron.SpecSchedule); ok {
		spec.Location = time.UTC
	}
	now = now.UTC()
	if next := sched.Next(now); !next.IsZero() && next.Sub(now) < constants.MinScheduledTaskFireInterval {
		return nil, fmt.Errorf("%w: must not fire more often than once per minute", domain.ErrScheduledTaskInvalidCron)
	}
	return sched, nil
}

// nextFireTime computes the next fire time strictly after now, in UTC.
// There is deliberately no fallback schedule: a broken expression surfaces
// as ErrScheduledTaskInvalidCron so the admin can fix it.
func nextFireTime(cronExpr string, now time.Time) (time.Time, error) {
	sched, err := parseCron(cronExpr, now)
	if err != nil {
		return time.Time{}, err
	}
	next := sched.Next(now.UTC())
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: schedule produced no next fire", domain.ErrScheduledTaskInvalidCron)
	}
	return next.UTC(), nil
}
