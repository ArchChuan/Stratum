package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// Proposer generates candidate patches from a diagnosis report, selecting
// and orchestrating optimization strategies based on affected categories.
type Proposer struct {
	strategies []domain.OptimizationStrategy
	tunables   []domain.Tunable
}

func NewProposer(strategies []domain.OptimizationStrategy, tunables []domain.Tunable) *Proposer {
	return &Proposer{strategies: strategies, tunables: tunables}
}

// Propose generates candidate patches targeting the tunables identified in
// the diagnosis report. Each strategy contributes patches for its supported
// categories; patches are annotated with diagnosis provenance and risk scores.
func (p *Proposer) Propose(
	ctx context.Context,
	baseline map[string]any,
	diagnosis DiagnosisReport,
	failures []domain.FailureSummary,
) ([]domain.CandidatePatch, error) {
	if len(diagnosis.AffectedTunables) == 0 {
		return nil, fmt.Errorf("propose: no affected tunables in diagnosis")
	}

	affectedSet := toSet(diagnosis.AffectedTunables)
	relevantTunables := filterTunables(p.tunables, affectedSet)
	if len(relevantTunables) == 0 {
		return nil, fmt.Errorf("propose: no tunables match affected set %v",
			diagnosis.AffectedTunables)
	}

	affectedCategories := tunablesToCategories(relevantTunables)
	strategies := filterStrategies(p.strategies, affectedCategories)
	if len(strategies) == 0 {
		return nil, fmt.Errorf("propose: no strategies cover categories %v",
			affectedCategories)
	}

	var allPatches []domain.CandidatePatch
	for _, strategy := range strategies {
		patches, err := strategy.Generate(ctx, baseline, relevantTunables, failures)
		if err != nil {
			return nil, fmt.Errorf("propose: strategy %s: %w", strategy.Name(), err)
		}
		for i := range patches {
			patches[i].DiagnosisRef = diagnosisRef(diagnosis)
			patches[i].RiskScore = estimateRisk(patches[i])
		}
		allPatches = append(allPatches, patches...)
	}

	if len(allPatches) == 0 {
		return nil, fmt.Errorf("propose: no patches generated")
	}
	if len(allPatches) > domain.MaxGeneratedCandidates {
		allPatches = allPatches[:domain.MaxGeneratedCandidates]
	}

	return allPatches, nil
}

// diagnosisRef builds a compact reference string from the primary hypothesis.
func diagnosisRef(d DiagnosisReport) string {
	if len(d.RootCauseHypotheses) == 0 {
		return "diagnosis:unknown"
	}
	primary := d.RootCauseHypotheses[0]
	return fmt.Sprintf("diagnosis:%s:%.0f", primary.Category, primary.Confidence*100)
}

// estimateRisk computes a heuristic risk score for a candidate patch.
// Parameter patches are lower risk than prompt rewrites.
func estimateRisk(p domain.CandidatePatch) float64 {
	switch {
	case len(p.PromptPatch) > 0:
		return 0.5 // Prompt changes carry moderate risk.
	case len(p.ParameterPatch) > 0:
		return 0.2 // Parameter changes are lower risk.
	default:
		return 0.3
	}
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func filterTunables(all []domain.Tunable, keys map[string]bool) []domain.Tunable {
	out := make([]domain.Tunable, 0, len(all))
	for _, t := range all {
		if keys[t.Key()] {
			out = append(out, t)
		}
	}
	return out
}

func tunablesToCategories(tunables []domain.Tunable) []domain.TunableCategory {
	seen := map[domain.TunableCategory]bool{}
	var cats []domain.TunableCategory
	for _, t := range tunables {
		cat := t.Category()
		if !seen[cat] {
			seen[cat] = true
			cats = append(cats, cat)
		}
	}
	return cats
}

func filterStrategies(
	all []domain.OptimizationStrategy,
	categories []domain.TunableCategory,
) []domain.OptimizationStrategy {
	catSet := make(map[domain.TunableCategory]bool, len(categories))
	for _, c := range categories {
		catSet[c] = true
	}
	out := make([]domain.OptimizationStrategy, 0, len(all))
	for _, s := range all {
		for _, sc := range s.Categories() {
			if catSet[sc] {
				out = append(out, s)
				break
			}
		}
	}
	return out
}
