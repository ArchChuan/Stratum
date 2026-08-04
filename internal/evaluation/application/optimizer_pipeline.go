package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// PipelineInput is the input to an optimizer pipeline run.
type PipelineInput struct {
	TenantID         string
	Baseline         domain.ResourceRef
	BaselineSnapshot map[string]any
	FailureSummaries []domain.FailureSummary
	CaseResults      []domain.EvalCaseResult
	SearchSpace      map[string][]any
}

// OptimizerPipeline orchestrates the four-stage optimization flow:
//
//	DIAGNOSE → PROPOSE → CRITIQUE → Gate
//
// DIAGNOSE classifies failures by tunable category.
// PROPOSE generates candidate patches targeting affected tunables.
// CRITIQUE runs adversarial review across safety/cost/regression/compatibility.
// Gate decides whether each candidate advances, is held, or is rejected.
type OptimizerPipeline struct {
	diagnoser *Diagnoser
	proposer  *Proposer
	critique  *CritiqueService
	gate      OptimizerGate
}

func NewOptimizerPipeline(
	diagnoser *Diagnoser,
	proposer *Proposer,
	critique *CritiqueService,
	gate OptimizerGate,
) *OptimizerPipeline {
	return &OptimizerPipeline{
		diagnoser: diagnoser,
		proposer:  proposer,
		critique:  critique,
		gate:      gate,
	}
}

// Run executes the full pipeline and returns a structured result.
// Each stage propagates errors; the pipeline stops at the first failure.
func (p *OptimizerPipeline) Run(
	ctx context.Context,
	input PipelineInput,
) (domain.PipelineResult, error) {
	// Stage 1: DIAGNOSE
	diagnosis, err := p.diagnoser.Diagnose(ctx, input.FailureSummaries, input.CaseResults)
	if err != nil {
		return domain.PipelineResult{}, fmt.Errorf("diagnose: %w", err)
	}
	if diagnosis.TotalFailures == 0 {
		return domain.PipelineResult{Diagnosis: diagnosis}, nil
	}

	// Stage 2: PROPOSE
	patches, err := p.proposer.Propose(ctx, input.BaselineSnapshot, diagnosis,
		input.FailureSummaries)
	if err != nil {
		return domain.PipelineResult{Diagnosis: diagnosis},
			fmt.Errorf("propose: %w", err)
	}

	// Stage 3: CRITIQUE (parallel per critic, sequential per patch)
	critiques, err := p.critique.ReviewAll(ctx, patches, input.BaselineSnapshot,
		diagnosis)
	if err != nil {
		return domain.PipelineResult{Diagnosis: diagnosis, Candidates: patches},
			fmt.Errorf("critique: %w", err)
	}

	// Stage 4: Gate
	decisions := make([]domain.GateDecision, len(patches))
	for i, patch := range patches {
		decisions[i] = p.gate.Decide(patch, critiques[i], nil)
	}

	return domain.PipelineResult{
		Diagnosis:  diagnosis,
		Candidates: patches,
		Critiques:  critiques,
		Decisions:  decisions,
	}, nil
}

// HasAdvancingCandidates returns true if at least one candidate received
// a GateAdvance decision.
func HasAdvancingCandidates(result domain.PipelineResult) bool {
	for _, d := range result.Decisions {
		if d == domain.GateAdvance {
			return true
		}
	}
	return false
}
