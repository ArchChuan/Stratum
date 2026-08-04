package application

import (
	"context"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/google/uuid"
)

// ExperimentRunner implements TenantJobRunner to auto-activate pending
// experiments and process running experiment stages. It complements the
// feedback-driven evaluation loop by ensuring experiments don't stall
// when no new feedback arrives.
type ExperimentRunner struct {
	experiments  *ExperimentService
	expRepo      port.ExperimentRepository
	feedbackRepo port.FeedbackRepository
}

func NewExperimentRunner(
	experiments *ExperimentService,
	expRepo port.ExperimentRepository,
	feedbackRepo port.FeedbackRepository,
) *ExperimentRunner {
	return &ExperimentRunner{experiments: experiments, expRepo: expRepo, feedbackRepo: feedbackRepo}
}

// RunOnce processes one tenant: activates pending experiments when no
// running experiment exists for the same resource, and evaluates running
// experiments that have accumulated enough observation time.
func (r *ExperimentRunner) RunOnce(
	ctx context.Context, tenantID, workerID string, lease time.Duration,
) (bool, error) {
	_ = workerID
	_ = lease

	activated, err := r.activatePending(ctx, tenantID)
	if err != nil {
		return activated, err
	}

	processed, err := r.processRunningStages(ctx, tenantID)
	return activated || processed, err
}

func (r *ExperimentRunner) activatePending(ctx context.Context, tenantID string) (bool, error) {
	pending, err := r.expRepo.ListPendingExperiments(ctx, tenantID, "", "")
	if err != nil {
		return false, err
	}
	if len(pending) == 0 {
		return false, nil
	}

	// Group by resource — only activate if no running experiment
	// exists for the same resource.
	seen := map[string]bool{}
	anyActivated := false
	for _, exp := range pending {
		resourceKey := string(exp.ResourceKind) + ":" + exp.ResourceID
		if seen[resourceKey] {
			continue
		}
		seen[resourceKey] = true

		active, err := r.expRepo.HasRunningExperiment(
			ctx, tenantID, string(exp.ResourceKind), exp.ResourceID,
		)
		if err != nil || active {
			continue
		}

		_, err = r.experiments.Activate(ctx, tenantID, exp.ID, ExperimentCommandInput{
			ActorID:              "experiment-worker",
			Reason:               "auto-activate pending experiment",
			IdempotencyKey:       uuid.NewString(),
			ExpectedStateVersion: exp.StateVersion,
		})
		if err != nil {
			if errors.Is(err, domain.ErrExperimentStateConflict) ||
				errors.Is(err, domain.ErrExperimentCommandNotAllowed) {
				continue
			}
			return anyActivated, err
		}
		anyActivated = true
	}
	return anyActivated, nil
}

func (r *ExperimentRunner) processRunningStages(ctx context.Context, tenantID string) (bool, error) {
	running, err := r.expRepo.ListRunningExperiments(ctx, tenantID)
	if err != nil {
		return false, err
	}

	anyProcessed := false
	for _, exp := range running {
		feedback, observedMinutes, err := r.feedbackRepo.StageFeedback(ctx, tenantID, exp)
		if err != nil {
			return anyProcessed, err
		}

		policy := exp.Policy
		if len(policy.Stages) == 0 {
			policy = domain.DefaultPromotionPolicy()
		}

		// Only evaluate when minimum observation criteria are met.
		if len(feedback) < policy.MinSamples || observedMinutes < policy.MinObservationMinutes {
			continue
		}

		// Rely on EvaluateStageIdempotent for idempotency — the
		// feedback-driven flow may have already evaluated this stage.
		_, _, err = r.experiments.EvaluateStage(ctx, tenantID, exp.ID, domain.StageMetrics{
			Samples:         len(feedback),
			ObservedMinutes: observedMinutes,
		})
		if err != nil {
			if errors.Is(err, domain.ErrExperimentCommandNotAllowed) {
				continue
			}
			return anyProcessed, err
		}
		anyProcessed = true
	}
	return anyProcessed, nil
}
