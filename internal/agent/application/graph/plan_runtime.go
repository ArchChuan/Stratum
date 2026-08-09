package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type PlanNodeExecutionResult struct {
	Summary             string
	UncertainSideEffect bool
}

type PlanNodeExecutor func(context.Context, ReActState, domain.PlanNode, map[string]string) (PlanNodeExecutionResult, error)

// schedulePlanWave computes the next ready wave of the active plan and
// registers it for engine execution via the plan slots. A wave larger than
// MaxPlanSteps is split: the excess stays pending and the LLM continues it
// with a further stratum_continue_plan call. With nothing ready it returns
// the plain observation, leaving the LLM to revise or cancel explicitly.
func schedulePlanWave(state *ReActState) (string, error) {
	if state == nil || state.ActivePlan == nil {
		return "", errors.New("plan runtime: active plan is required")
	}
	ready, err := planWaveReady(state.ActivePlan)
	if err != nil {
		return "", err
	}
	if len(ready) == 0 {
		return planObservation("stratum_continue_plan", state.ActivePlan), nil
	}
	if len(ready) > constants.MaxPlanSteps {
		ready = ready[:constants.MaxPlanSteps]
	}
	if state.PlanLimits.MaxRevisions > 0 && state.ActivePlan.Revision+int64(len(ready)) > state.PlanLimits.MaxRevisions {
		return "", fmt.Errorf("%w: ready wave requires %d revisions", domain.ErrPlanBudgetExceeded, len(ready))
	}
	state.PlanWavePending = ready
	return planObservation("stratum_continue_plan", state.ActivePlan), nil
}

// planWaveReady returns the indices of pending nodes whose dependencies all
// succeeded. Structure is validated up front (duplicate IDs, missing or
// repeated deps, cycles); blocked propagation is intentionally dropped — the
// LLM observes pending status and revises explicitly, matching the previous
// scheduler's externally visible behaviour.
func planWaveReady(plan *domain.Plan) ([]int, error) {
	if err := validatePlanStructure(plan); err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(plan.Nodes))
	for i, node := range plan.Nodes {
		byID[node.ID] = i
	}
	ready := make([]int, 0)
	for i, node := range plan.Nodes {
		if node.Status != domain.PlanNodeStatusPending {
			continue
		}
		if allPlanDepsSucceeded(plan, byID, node) {
			ready = append(ready, i)
		}
	}
	return ready, nil
}

// validatePlanStructure rejects malformed plans and dependency cycles,
// mirroring the former dag-backed scheduler's validation.
func validatePlanStructure(plan *domain.Plan) error {
	byID := make(map[string]int, len(plan.Nodes))
	if err := validateNodeUniqueness(plan, byID); err != nil {
		return err
	}
	indegree := make([]int, len(plan.Nodes))
	children := make([][]int, len(plan.Nodes))
	if err := validateNodeDependencies(plan, byID, indegree, children); err != nil {
		return err
	}
	return detectDependencyCycle(plan, indegree, children)
}

// validateNodeUniqueness builds the ID index, rejecting duplicate IDs.
func validateNodeUniqueness(plan *domain.Plan, byID map[string]int) error {
	for i, node := range plan.Nodes {
		if _, exists := byID[node.ID]; exists {
			return fmt.Errorf("plan runtime: duplicate node %q", node.ID)
		}
		byID[node.ID] = i
	}
	return nil
}

// validateNodeDependencies rejects self, missing, and repeated dependencies,
// filling the indegree and children adjacency for the cycle check.
func validateNodeDependencies(plan *domain.Plan, byID map[string]int, indegree []int, children [][]int) error {
	for i, node := range plan.Nodes {
		seen := make(map[string]struct{}, len(node.DependsOn))
		for _, dep := range node.DependsOn {
			if dep == node.ID {
				return fmt.Errorf("plan runtime: node %q depends on itself", node.ID)
			}
			depIdx, exists := byID[dep]
			if !exists {
				return fmt.Errorf("plan runtime: node %q depends on missing node %q", node.ID, dep)
			}
			if _, repeated := seen[dep]; repeated {
				return fmt.Errorf("plan runtime: node %q repeats dependency %q", node.ID, dep)
			}
			seen[dep] = struct{}{}
			indegree[i]++
			children[depIdx] = append(children[depIdx], i)
		}
	}
	return nil
}

// detectDependencyCycle runs Kahn's algorithm; any node left unvisited is
// part of a dependency cycle.
func detectDependencyCycle(plan *domain.Plan, indegree []int, children [][]int) error {
	queue := make([]int, 0, len(plan.Nodes))
	for i, node := range plan.Nodes {
		if len(node.DependsOn) == 0 {
			queue = append(queue, i)
		}
	}
	visited := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		visited++
		for _, child := range children[current] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(plan.Nodes) {
		return fmt.Errorf("plan runtime: dependency cycle")
	}
	return nil
}

func allPlanDepsSucceeded(plan *domain.Plan, byID map[string]int, node domain.PlanNode) bool {
	for _, dep := range node.DependsOn {
		if plan.Nodes[byID[dep]].Status != domain.PlanNodeStatusSucceeded {
			return false
		}
	}
	return true
}

// applyPlanOutcomes folds one wave of plan node outcomes into the active plan:
// per-node status transition, attempt record, revision bump, and per-node
// checkpoint. A checkpoint failure aborts the fold and propagates — the plan
// may then be partially advanced but the whole execution is reported failed.
// It returns the IDs of nodes that failed in this wave.
func applyPlanOutcomes(ctx context.Context, state *ReActState, outcomes []PlanWaveOutcome) ([]string, error) {
	if state == nil || state.ActivePlan == nil {
		return nil, errors.New("plan runtime: active plan is required")
	}
	if state.CheckpointEnabled && state.PlanCheckpointWriter == nil {
		return nil, ErrPlanCheckpointRequired
	}
	if state.PlanIDSource == nil {
		return nil, errors.New("plan runtime: ID source is required")
	}
	failed := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		failedWave, err := applyPlanOutcome(ctx, state, outcome)
		if err != nil {
			return failed, err
		}
		failed = append(failed, failedWave...)
	}
	return failed, nil
}

// applyPlanOutcome folds one outcome: status transition, attempt record,
// revision bump, and the per-node wave checkpoint.
func applyPlanOutcome(ctx context.Context, state *ReActState, outcome PlanWaveOutcome) ([]string, error) {
	node := findPlanNode(state.ActivePlan, outcome.NodeID)
	if node == nil {
		return nil, nil
	}
	attempt := domain.PlanAttempt{ID: state.PlanIDSource(), Number: len(node.Attempts) + 1}
	var failed []string
	if outcome.Status == domain.PlanNodeStatusSucceeded {
		node.Status = domain.PlanNodeStatusSucceeded
		attempt.Summary = outcome.Summary
	} else {
		node.Status = outcome.Status
		attempt.Error = outcomeErrorText(outcome)
		failed = []string{outcome.NodeID}
	}
	node.Attempts = append(node.Attempts, attempt)
	state.ActivePlan.Revision++
	if state.CheckpointEnabled {
		if err := persistWaveCheckpoint(ctx, state, node); err != nil {
			return failed, err
		}
	}
	return failed, nil
}

// persistWaveCheckpoint writes the per-node wave checkpoint with the
// wave-format ID ${planID}-wave-${revision}-${nodeID}.
func persistWaveCheckpoint(ctx context.Context, state *ReActState, node *domain.PlanNode) error {
	identity := state.PlanCheckpointIdentity
	identity.CheckpointID = fmt.Sprintf("%s-wave-%d-%s", state.ActivePlan.ID, state.ActivePlan.Revision, node.ID)
	return PersistPlanCheckpoint(ctx, state.PlanCheckpointWriter, state.TenantID, identity, PlanCheckpointPayload{
		Plan:                    state.ActivePlan,
		RemainingNodeBudget:     state.PlanLimits.MaxNodes - len(state.ActivePlan.Nodes),
		RemainingRevisionBudget: state.PlanLimits.MaxRevisions - state.ActivePlan.Revision,
	}, checkpointSnapshot(state))
}

func findPlanNode(plan *domain.Plan, id string) *domain.PlanNode {
	for index := range plan.Nodes {
		if plan.Nodes[index].ID == id {
			return &plan.Nodes[index]
		}
	}
	return nil
}

func outcomeErrorText(outcome PlanWaveOutcome) string {
	if outcome.Err != "" {
		return outcome.Err
	}
	return "uncertain external side effect"
}

func dependencySummaries(plan *domain.Plan, node domain.PlanNode) map[string]string {
	summaries := make(map[string]string, len(node.DependsOn))
	for _, dependencyID := range node.DependsOn {
		for _, dependency := range plan.Nodes {
			if dependency.ID != dependencyID || len(dependency.Attempts) == 0 {
				continue
			}
			summaries[dependencyID] = dependency.Attempts[len(dependency.Attempts)-1].Summary
		}
	}
	return summaries
}
