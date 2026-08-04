package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/internal/collab/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// Worker claims task steps across tenants and executes them through the
// agent runner. At-least-once: a step whose finalize write fails keeps its
// lease, expires, and is reclaimed for a retry.
type Worker struct {
	owner   string
	collabs port.CollaborationRepo
	steps   port.TaskStepRepo
	shared  port.SharedContextRepo
	runner  port.AgentRunner
	lease   time.Duration
	metrics observability.MetricsProvider
}

// NewWorker wires the collab task worker.
func NewWorker(owner string, collabs port.CollaborationRepo, steps port.TaskStepRepo, shared port.SharedContextRepo, runner port.AgentRunner, lease time.Duration, metrics observability.MetricsProvider) *Worker {
	return &Worker{owner: owner, collabs: collabs, steps: steps, shared: shared, runner: runner, lease: lease, metrics: metrics}
}

// RunOnce claims one ready step and executes it. Reports whether a step was
// claimed (so the caller can immediately retry for more work).
func (w *Worker) RunOnce(ctx context.Context) bool {
	tenantID, step, ok, err := w.steps.ClaimNextTask(ctx, w.owner, w.lease)
	if err != nil || !ok {
		return false
	}
	execCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go w.heartbeat(execCtx, cancel, done, tenantID, step.ID)
	start := time.Now()
	output, _, runErr := w.runner.RunAgentStep(execCtx, tenantID, step.AgentID, step.Input)
	close(done)
	cancel()
	duration := time.Since(start).Seconds()

	w.finalizeStep(ctx, tenantID, step, output, runErr)
	// Plan-level metrics and completion judgment are best-effort: a plan that
	// vanished mid-run yields no strategy to record, which is tolerable.
	plan, perr := w.collabs.GetByID(ctx, tenantID, step.PlanID)
	if perr != nil || plan == nil {
		return true
	}
	w.metrics.RecordCollabTaskDuration(string(plan.Strategy), duration)
	w.judgePlanCompletion(ctx, tenantID, plan)
	return true
}

// finalizeStep persists the execution outcome, aggregating successful output
// into the shared context (bounded optimistic-lock retries, best-effort —
// the step row remains the source of truth).
func (w *Worker) finalizeStep(ctx context.Context, tenantID string, step *domain.TaskStep, output map[string]any, runErr error) {
	if runErr == nil {
		w.aggregateSharedContext(ctx, tenantID, step, output)
		_ = w.steps.UpdateStatus(ctx, tenantID, step.ID, step.Generation, domain.TaskCompleted, output, "")
		return
	}
	errMsg := truncateError(runErr)
	if step.RetryCount+1 < step.MaxRetries {
		// Release back to pending: retry_count is bumped by the repo.
		_ = w.steps.UpdateStatus(ctx, tenantID, step.ID, step.Generation, domain.TaskPending, nil, errMsg)
		return
	}
	_ = w.steps.UpdateStatus(ctx, tenantID, step.ID, step.Generation, domain.TaskFailed, nil, errMsg)
}

// aggregateSharedContext merges the step output into plan shared state with
// optimistic-lock retries. The first writer upserts the row (version 0); a
// concurrent first-insert surfaces as a conflict and is retried. Best-effort:
// persistent failure leaves the step completed with its own output intact.
func (w *Worker) aggregateSharedContext(ctx context.Context, tenantID string, step *domain.TaskStep, output map[string]any) {
	for attempt := 0; attempt < sharedContextUpdateMaxRetries; attempt++ {
		sc, err := w.shared.Get(ctx, tenantID, step.PlanID)
		if err != nil {
			return
		}
		merged := buildMergedContext(sc, step.PlanID, step.AgentID, output)
		if err := w.shared.Update(ctx, tenantID, *merged); err == nil {
			return
		} else if !errors.Is(err, domain.ErrCollabConflict) {
			return
		}
	}
}

// judgePlanCompletion marks the plan completed|failed once every step is
// terminal. RowsAffected == 0 is tolerated: the plan was canceled
// concurrently and the worker must not resurrect it.
func (w *Worker) judgePlanCompletion(ctx context.Context, tenantID string, plan *domain.Collaboration) {
	counts, err := w.steps.CountByStatus(ctx, tenantID, plan.ID)
	if err != nil {
		return
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return
	}
	terminal := counts[domain.TaskCompleted] + counts[domain.TaskFailed] + counts[domain.TaskCanceled]
	if terminal != total {
		return
	}
	now := time.Now()
	// Apply exactly one terminal migration: failed beats completed.
	if counts[domain.TaskFailed] > 0 {
		if err := plan.Fail(now); err != nil {
			// not running (e.g. canceled): nothing to judge
			return
		}
	} else if err := plan.Complete(now); err != nil {
		return
	}
	if err := w.collabs.UpdateStatus(ctx, tenantID, plan.ID, plan.Status, nil, plan.CompletedAt); err != nil {
		return
	}
	outcome := "completed"
	if plan.Status == domain.CollabFailed {
		outcome = "failed"
	}
	w.metrics.IncCollabPlan(string(plan.Strategy), outcome)
}

// heartbeat extends the step lease until execution finishes. A renewal
// failure cancels execution: the lease expires and the step is reclaimed.
func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, tenantID, stepID string) {
	interval := w.lease / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := w.steps.RenewLease(ctx, tenantID, stepID, w.owner, w.lease); err != nil {
				cancel()
				return
			}
		}
	}
}

// Run loops RunOnce with an idle backoff until ctx is canceled.
func (w *Worker) Run(ctx context.Context, idle time.Duration) {
	if idle <= 0 {
		idle = 250 * time.Millisecond
	}
	ticker := time.NewTicker(idle)
	defer ticker.Stop()
	for {
		if w.RunOnce(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// buildMergedContext writes the step output under the agent id, preserving
// existing entries and capping the per-entry size. A missing context starts
// fresh at version 0 (first writer upserts the row).
func buildMergedContext(sc *domain.SharedContext, planID, agentID string, output map[string]any) *domain.SharedContext {
	data := map[string]any{}
	if sc != nil && len(sc.Data) > 0 {
		// Corrupt cache entries are dropped, never propagated.
		_ = json.Unmarshal(sc.Data, &data)
	}
	data[agentID] = sanitizeOutput(output)
	b, _ := json.Marshal(data)
	version := 0
	if sc != nil {
		version = sc.Version
	}
	return &domain.SharedContext{PlanID: planID, Data: b, Version: version}
}

// sanitizeOutput caps an entry so plan-wide context stays bounded
// (MaxCollabParticipants × SharedContextMaxBytes).
func sanitizeOutput(output map[string]any) map[string]any {
	if output == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(output)
	if err != nil {
		return map[string]any{"truncated": true}
	}
	if len(b) <= SharedContextMaxBytes {
		return output
	}
	return map[string]any{
		"truncated": true,
		"note":      fmt.Sprintf("output exceeds %d bytes", SharedContextMaxBytes),
	}
}

// truncateError caps error text persisted on a step.
func truncateError(err error) string {
	r := []rune(err.Error())
	if len(r) <= StepErrorMaxRunes {
		return err.Error()
	}
	return string(r[:StepErrorMaxRunes])
}
