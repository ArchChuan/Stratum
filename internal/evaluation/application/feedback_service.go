package application

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

type RecordFeedbackInput = domain.FeedbackRequest

type FeedbackResult struct {
	Feedback   domain.EvaluationFeedback `json:"feedback"`
	Experiment *domain.Experiment        `json:"experiment,omitempty"`
	Decision   domain.Decision           `json:"decision"`
}

type FeedbackService struct {
	repo        port.FeedbackRepository
	experiments *ExperimentService
	evidence    port.TraceEvidenceReader
}

func NewFeedbackService(
	repo port.FeedbackRepository, experiments *ExperimentService, evidence port.TraceEvidenceReader,
) *FeedbackService {
	return &FeedbackService{repo: repo, experiments: experiments, evidence: evidence}
}

func (s *FeedbackService) Record(
	ctx context.Context,
	tenantID string,
	input RecordFeedbackInput,
) (FeedbackResult, error) {
	if input.TraceID == "" || input.ResourceID == "" || input.IdempotencyKey == "" {
		return FeedbackResult{}, errors.New("trace id, resource id and idempotency key are required")
	}
	if input.Score < 0 || input.Score > 1 {
		return FeedbackResult{}, errors.New("feedback score must be between 0 and 1")
	}
	if s.evidence == nil {
		return FeedbackResult{}, errors.New("trace evidence unavailable")
	}
	observed, err := s.evidence.Resolve(ctx, tenantID, input.TraceID)
	if err != nil {
		return FeedbackResult{}, err
	}
	assignment, ok := observed.Assignments[string(input.ResourceKind)+":"+input.ResourceID]
	if !ok || assignment.RevisionID == "" {
		return FeedbackResult{}, errors.New("trace resource assignment not found")
	}
	input.RevisionID = assignment.RevisionID
	input.ExperimentID = assignment.ExperimentID
	input.Variant = assignment.Variant
	feedback, err := s.repo.Record(ctx, tenantID, input)
	if err != nil {
		return FeedbackResult{}, err
	}
	result := FeedbackResult{Feedback: feedback, Decision: domain.DecisionHold}
	experiment, ok, err := s.repo.ActiveExperiment(ctx, tenantID, string(input.ResourceKind), input.ResourceID)
	if err != nil || !ok {
		return result, err
	}
	if observed.SecurityViolation || input.SecurityViolation || feedbackSecurityViolation(feedback) {
		next, decision, err := s.experiments.EvaluateStageIdempotent(ctx, tenantID, experiment.ID, EvaluateStageInput{
			Metrics:        domain.StageMetrics{SecurityViolation: true},
			IdempotencyKey: evaluationIdempotencyKey(input.IdempotencyKey, experiment.ID),
		})
		if err != nil {
			return FeedbackResult{}, err
		}
		result.Experiment, result.Decision = &next, decision
		return result, nil
	}
	feedbackRows, observedMinutes, err := s.repo.StageFeedback(ctx, tenantID, experiment)
	if err != nil {
		return FeedbackResult{}, err
	}
	stable, canary, err := s.observations(ctx, tenantID, experiment, feedbackRows)
	if err != nil {
		return FeedbackResult{}, err
	}
	policy := experiment.Policy
	if len(policy.Stages) == 0 {
		policy = domain.DefaultPromotionPolicy()
	}
	if len(stable) < policy.MinSamples || len(canary) < policy.MinSamples {
		partialSamples := len(stable)
		if len(canary) < partialSamples {
			partialSamples = len(canary)
		}
		next, decision, evaluateErr := s.experiments.EvaluateStageIdempotent(ctx, tenantID, experiment.ID,
			EvaluateStageInput{Metrics: domain.StageMetrics{Samples: partialSamples, ObservedMinutes: observedMinutes},
				IdempotencyKey: evaluationIdempotencyKey(input.IdempotencyKey, experiment.ID)})
		if evaluateErr != nil {
			return FeedbackResult{}, evaluateErr
		}
		result.Experiment, result.Decision = &next, decision
		return result, nil
	}
	stableScores, canaryScores := scores(stable), scores(canary)
	improvement, significant, err := domain.BootstrapQualityDifference(stableScores, canaryScores, 1000)
	if err != nil {
		return FeedbackResult{}, err
	}
	metrics := domain.StageMetrics{
		Samples: len(stable) + len(canary), ObservedMinutes: observedMinutes,
		QualityImprovement: improvement, QualitySignificant: significant,
		CostRegression:       relativeRegression(meanCost(stable), meanCost(canary)),
		P95LatencyRegression: relativeRegression(p95Latency(stable), p95Latency(canary)),
		ErrorRateIncrease:    errorRate(canary) - errorRate(stable),
		SecurityViolation:    hasSecurityViolation(stable) || hasSecurityViolation(canary),
	}
	next, decision, err := s.experiments.EvaluateStageIdempotent(ctx, tenantID, experiment.ID, EvaluateStageInput{
		Metrics: metrics, IdempotencyKey: evaluationIdempotencyKey(input.IdempotencyKey, experiment.ID),
	})
	if err != nil {
		return FeedbackResult{}, err
	}
	result.Experiment = &next
	result.Decision = decision
	return result, nil
}

func evaluationIdempotencyKey(feedbackKey, experimentID string) string {
	return fmt.Sprintf("feedback:%s:%s", feedbackKey, experimentID)
}

func (s *FeedbackService) observations(
	ctx context.Context,
	tenantID string,
	experiment domain.Experiment,
	feedbackRows []domain.EvaluationFeedback,
) ([]domain.OnlineObservation, []domain.OnlineObservation, error) {
	traceIDs := make([]string, 0, len(feedbackRows))
	for _, feedback := range feedbackRows {
		traceIDs = append(traceIDs, feedback.TraceID)
	}
	traces, err := s.evidence.ResolveBatch(ctx, tenantID, traceIDs)
	if err != nil {
		return nil, nil, err
	}
	var stable, canary []domain.OnlineObservation
	resourceKey := string(experiment.ResourceKind) + ":" + experiment.ResourceID
	for _, feedback := range feedbackRows {
		trace, ok := traces[feedback.TraceID]
		if !ok {
			return nil, nil, errors.New("trace evidence unavailable")
		}
		assignment, ok := trace.Assignments[resourceKey]
		if !ok || assignment.RevisionID != feedback.RevisionID {
			return nil, nil, errors.New("trace evidence assignment mismatch")
		}
		observation := domain.OnlineObservation{
			Score: feedback.Score, CostUSD: trace.CostUSD, LatencyMs: trace.LatencyMs,
			Success: trace.Success, SecurityViolation: trace.SecurityViolation || feedbackSecurityViolation(feedback),
		}
		switch feedback.RevisionID {
		case experiment.StableRevisionID:
			stable = append(stable, observation)
		case experiment.CanaryRevisionID:
			canary = append(canary, observation)
		}
	}
	return stable, canary, nil
}

func feedbackSecurityViolation(feedback domain.EvaluationFeedback) bool {
	value, _ := feedback.Outcome["security_violation"].(bool)
	return value
}

func hasSecurityViolation(observations []domain.OnlineObservation) bool {
	for _, observation := range observations {
		if observation.SecurityViolation {
			return true
		}
	}
	return false
}

func scores(observations []domain.OnlineObservation) []float64 {
	out := make([]float64, len(observations))
	for i, observation := range observations {
		out[i] = observation.Score
	}
	return out
}

func meanCost(observations []domain.OnlineObservation) float64 {
	total := 0.0
	for _, observation := range observations {
		total += observation.CostUSD
	}
	return total / float64(len(observations))
}

func p95Latency(observations []domain.OnlineObservation) float64 {
	values := make([]int64, len(observations))
	for i, observation := range observations {
		values[i] = observation.LatencyMs
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(math.Ceil(float64(len(values))*0.95)) - 1
	return float64(values[index])
}

func errorRate(observations []domain.OnlineObservation) float64 {
	errorsCount := 0
	for _, observation := range observations {
		if !observation.Success {
			errorsCount++
		}
	}
	return float64(errorsCount) / float64(len(observations))
}

func relativeRegression(stable, canary float64) float64 {
	if stable == 0 {
		if canary == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return canary/stable - 1
}
