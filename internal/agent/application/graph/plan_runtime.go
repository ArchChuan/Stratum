package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/dag"
)

type PlanNodeExecutionResult struct {
	Summary             string
	UncertainSideEffect bool
}

type PlanNodeExecutor func(context.Context, ReActState, domain.PlanNode, map[string]string) (PlanNodeExecutionResult, error)

// ExecuteReadyPlanNodes executes exactly one ready wave and persists every
// node transition before exposing the resulting parent observation.
func ExecuteReadyPlanNodes(ctx context.Context, state *ReActState, execute PlanNodeExecutor) (string, error) {
	if state == nil || state.ActivePlan == nil {
		return "", fmt.Errorf("plan runtime: active plan is required")
	}
	if execute == nil {
		return "", fmt.Errorf("plan runtime: executor is required")
	}
	if state.PlanCheckpointWriter == nil {
		return "", ErrPlanCheckpointRequired
	}
	plan := cloneRuntimePlan(state.ActivePlan)
	ready, _, _, err := readyPlanNodes(plan)
	if err != nil {
		return "", err
	}
	if len(ready) == 0 {
		return planObservation("stratum_continue_plan", plan), nil
	}
	if state.PlanLimits.MaxRevisions > 0 && plan.Revision+int64(len(ready)) > state.PlanLimits.MaxRevisions {
		return "", fmt.Errorf("%w: ready wave requires %d revisions", domain.ErrPlanBudgetExceeded, len(ready))
	}
	if state.PlanIDSource == nil {
		return "", fmt.Errorf("plan runtime: ID source is required")
	}
	limit := state.PlanLimits.MaxConcurrentNodes
	if limit <= 0 || limit > len(ready) {
		limit = len(ready)
	}
	waveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, limit)
	type result struct {
		nodeID string
		result PlanNodeExecutionResult
		err    error
	}
	results := make(chan result, len(ready))
	var workers sync.WaitGroup
	for _, node := range ready {
		node := node
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case sem <- struct{}{}:
			case <-waveCtx.Done():
				results <- result{nodeID: node.ID, err: waveCtx.Err()}
				return
			}
			defer func() { <-sem }()
			var output PlanNodeExecutionResult
			var execErr error
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						execErr = fmt.Errorf("node panic: %v", recovered)
					}
				}()
				childState := *state
				childState.ActivePlan = nil
				childState.PlanToolsDisabled = true
				output, execErr = execute(waveCtx, childState, node, dependencySummaries(plan, node))
			}()
			results <- result{nodeID: node.ID, result: output, err: execErr}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	for item := range results {
		var node *domain.PlanNode
		for index := range plan.Nodes {
			if plan.Nodes[index].ID == item.nodeID {
				node = &plan.Nodes[index]
				break
			}
		}
		if node == nil {
			continue
		}
		if item.err != nil || item.result.UncertainSideEffect {
			node.Status = domain.PlanNodeStatusFailed
			if item.result.UncertainSideEffect {
				node.Status = domain.PlanNodeStatusFailedPendingConfirmation
			}
			errText := "uncertain external side effect"
			if item.err != nil {
				errText = item.err.Error()
			}
			node.Attempts = append(node.Attempts, domain.PlanAttempt{ID: state.PlanIDSource(), Number: len(node.Attempts) + 1, Error: errText})
		} else {
			node.Status = domain.PlanNodeStatusSucceeded
			node.Attempts = append(node.Attempts, domain.PlanAttempt{ID: state.PlanIDSource(), Number: len(node.Attempts) + 1, Summary: item.result.Summary})
		}
		plan.Revision++
		identity := state.PlanCheckpointIdentity
		identity.CheckpointID = fmt.Sprintf("%s-wave-%d-%s", plan.ID, plan.Revision, node.ID)
		if err := PersistPlanCheckpoint(ctx, state.PlanCheckpointWriter, state.TenantID, identity, PlanCheckpointPayload{
			Plan: plan, RemainingNodeBudget: state.PlanLimits.MaxNodes - len(plan.Nodes), RemainingRevisionBudget: state.PlanLimits.MaxRevisions - plan.Revision,
		}); err != nil {
			cancel()
			for range results {
				// Drain worker results after cancellation so every goroutine can exit.
			}
			return "", err
		}
	}
	state.ActivePlan = plan
	return planObservation("stratum_continue_plan", plan), nil
}

func readyPlanNodes(plan *domain.Plan) ([]domain.PlanNode, []string, bool, error) {
	nodes := make([]dag.Node, 0, len(plan.Nodes))
	statuses := make(map[string]dag.Status, len(plan.Nodes))
	byID := make(map[string]domain.PlanNode, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes = append(nodes, dag.Node{ID: node.ID, DependsOn: node.DependsOn})
		byID[node.ID] = node
		switch node.Status {
		case domain.PlanNodeStatusSucceeded:
			statuses[node.ID] = dag.StatusSucceeded
		case domain.PlanNodeStatusFailed, domain.PlanNodeStatusFailedPendingConfirmation:
			statuses[node.ID] = dag.StatusFailed
		case domain.PlanNodeStatusBlocked:
			statuses[node.ID] = dag.StatusBlocked
		case domain.PlanNodeStatusCancelled:
			statuses[node.ID] = dag.StatusCancelled
		case domain.PlanNodeStatusRunning:
			statuses[node.ID] = dag.StatusRunning
		}
	}
	readyIDs, blocked, complete, err := dag.Ready(dag.Snapshot{Nodes: nodes, Statuses: statuses})
	ready := make([]domain.PlanNode, 0, len(readyIDs))
	for _, id := range readyIDs {
		ready = append(ready, byID[id])
	}
	return ready, blocked, complete, err
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

func cloneRuntimePlan(plan *domain.Plan) *domain.Plan {
	encoded, _ := json.Marshal(plan)
	var cloned domain.Plan
	_ = json.Unmarshal(encoded, &cloned)
	return &cloned
}
