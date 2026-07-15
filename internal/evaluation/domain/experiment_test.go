package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExperimentJSONUsesAPISnakeCaseFields(t *testing.T) {
	payload, err := json.Marshal(Experiment{
		ID:     "experiment-1",
		Status: ExperimentRunning,
		Stage:  5,
		Policy: DefaultPromotionPolicy(),
	})
	if err != nil {
		t.Fatalf("marshal experiment: %v", err)
	}

	body := string(payload)
	for _, field := range []string{`"id"`, `"status"`, `"stage"`, `"min_samples"`} {
		if !strings.Contains(body, field) {
			t.Fatalf("expected %s in API JSON: %s", field, body)
		}
	}
	for _, field := range []string{`"ID"`, `"Status"`, `"Stage"`, `"MinSamples"`} {
		if strings.Contains(body, field) {
			t.Fatalf("unexpected Go field %s in API JSON: %s", field, body)
		}
	}
}

func TestAssignVariantIsStableAndRespectsBounds(t *testing.T) {
	key := "tenant-1:conversation-9:skill-3"
	first := AssignVariant(key, 20)
	for range 10 {
		if got := AssignVariant(key, 20); got != first {
			t.Fatalf("assignment changed: first=%v got=%v", first, got)
		}
	}
	if AssignVariant(key, 0) {
		t.Fatal("zero percent must never select canary")
	}
	if !AssignVariant(key, 100) {
		t.Fatal("one hundred percent must always select canary")
	}
}

func TestExperimentAdvanceRequiresEnoughEvidence(t *testing.T) {
	exp := Experiment{Status: ExperimentRunning, Stage: 5}
	policy := DefaultPromotionPolicy()

	next, decision := exp.Decide(StageMetrics{
		Samples:         policy.MinSamples - 1,
		ObservedMinutes: policy.MinObservationMinutes,
	}, policy)

	if decision != DecisionHold || next.Stage != 5 {
		t.Fatalf("expected hold at 5%%, got decision=%s stage=%d", decision, next.Stage)
	}
}

func TestExperimentRollsBackOnGuardrailBreach(t *testing.T) {
	exp := Experiment{Status: ExperimentRunning, Stage: 20}
	policy := DefaultPromotionPolicy()

	next, decision := exp.Decide(StageMetrics{
		Samples:              policy.MinSamples,
		ObservedMinutes:      policy.MinObservationMinutes,
		QualityImprovement:   0.08,
		QualitySignificant:   true,
		CostRegression:       policy.MaxCostRegression + 0.01,
		P95LatencyRegression: 0,
		ErrorRateIncrease:    0,
	}, policy)

	if decision != DecisionRollback || next.Status != ExperimentRolledBack {
		t.Fatalf("expected rollback, got decision=%s status=%s", decision, next.Status)
	}
}

func TestExperimentAdvancesThroughConfiguredStages(t *testing.T) {
	policy := DefaultPromotionPolicy()
	metrics := StageMetrics{
		Samples:            policy.MinSamples,
		ObservedMinutes:    policy.MinObservationMinutes,
		QualityImprovement: 0.05,
		QualitySignificant: true,
	}

	exp := Experiment{Status: ExperimentRunning, Stage: 5}
	wantStages := []int{20, 50, 100}
	for _, want := range wantStages {
		next, decision := exp.Decide(metrics, policy)
		if decision != DecisionPromote || next.Stage != want {
			t.Fatalf("expected promotion to %d, got decision=%s stage=%d", want, decision, next.Stage)
		}
		exp = next
	}
	if exp.Status != ExperimentCompleted {
		t.Fatalf("expected completed at 100%%, got %s", exp.Status)
	}
}

func TestExperimentCannotPromoteWithoutQualitySignal(t *testing.T) {
	policy := DefaultPromotionPolicy()
	exp := Experiment{Status: ExperimentRunning, Stage: 50}

	next, decision := exp.Decide(StageMetrics{
		Samples:            policy.MinSamples,
		ObservedMinutes:    policy.MinObservationMinutes,
		QualityImprovement: 0.10,
	}, policy)

	if decision != DecisionHold || next.Stage != 50 {
		t.Fatalf("expected hold without significant quality signal, got decision=%s stage=%d", decision, next.Stage)
	}
}
