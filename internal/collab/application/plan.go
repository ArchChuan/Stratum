package application

import (
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
)

// generateSteps derives the task DAG for a plan. sequential/pipeline/
// hierarchical chain steps (each depends on its predecessor); parallel/swarm
// produce independent steps. Deterministic given the same inputs.
func generateSteps(planID string, participants []string, strategy domain.CollabStrategy, taskDesc string, now time.Time, newID func() string) []domain.TaskStep {
	steps := make([]domain.TaskStep, 0, len(participants))
	var previous string
	for i, participant := range participants {
		step := domain.TaskStep{
			ID:         newID(),
			PlanID:     planID,
			AgentID:    participant,
			Status:     domain.TaskPending,
			Delegation: "no_delegate", // collab steps run without delegation escalation
			MaxRetries: DefaultStepMaxRetries,
			Input: map[string]any{
				"query":       taskDesc,
				"plan_id":     planID, // consumed by the runner for plan-scoped identity
				"step_index":  i,
				"total_steps": len(participants),
			},
			CreatedAt: now,
			UpdatedAt: now,
		}
		switch strategy {
		case domain.CollabSequential, domain.CollabPipeline, domain.CollabHierarchical:
			if previous != "" {
				step.Dependencies = []string{previous}
			}
			previous = step.ID
		}
		steps = append(steps, step)
	}
	return steps
}
