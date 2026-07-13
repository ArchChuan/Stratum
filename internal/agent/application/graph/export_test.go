// Test-only exports for the graph package — visible to graph_test but not compiled into production.

package graph

import (
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// ExportBuildWaves exposes buildWaves for white-box wave-scheduling tests.
func ExportBuildWaves(plan []domain.PlanStep) [][]int {
	return buildWaves(plan)
}

// ExportBuildStepState exposes buildStepState for context-isolation tests.
func ExportBuildStepState(parent ReActState, step domain.PlanStep, stepIdx int, results []domain.StepResult) ReActState {
	// Convert results to the full slice length expected by buildStepState
	full := make([]domain.StepResult, stepIdx)
	copy(full, results)
	return buildStepState(parent, step, stepIdx, full)
}

// ExportIsStuck exposes isStuck for threshold boundary tests.
func ExportIsStuck(s ReActState) bool {
	return isStuck(s)
}

// Ensure port import is used (buildStepState uses port.LLMMessage internally).
var _ port.LLMMessage
