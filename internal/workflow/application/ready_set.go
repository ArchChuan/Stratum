package application

import (
	"slices"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/pkg/dag"
)

// ReadySet deterministically computes executable and unreachable nodes from
// the immutable run snapshot plus append-only attempt outcomes.
func ReadySet(spec domain.Spec, attempts []domain.NodeAttempt) ([]domain.Node, []string) {
	latest := make(map[string]domain.NodeAttempt)
	for _, attempt := range attempts {
		if current, ok := latest[attempt.NodeID]; !ok || attempt.AttemptNo >= current.AttemptNo {
			latest[attempt.NodeID] = attempt
		}
	}
	incoming := make(map[string][]domain.Edge)
	for _, edge := range spec.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge)
	}
	selected := func(edge domain.Edge) (resolved, chosen bool) {
		parent, ok := latest[edge.From]
		if !ok || (parent.Status != domain.AttemptStatusSucceeded && parent.Status != domain.AttemptStatusSkipped) {
			return false, false
		}
		if parent.Status == domain.AttemptStatusSkipped {
			return true, false
		}
		if len(parent.SelectedEdges) == 0 {
			return true, true
		}
		for _, id := range parent.SelectedEdges {
			if id == edge.ID || (id == "" && edge.ID == "") {
				return true, true
			}
		}
		return true, false
	}

	skippedSet := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, node := range spec.Nodes {
			if _, exists := latest[node.ID]; exists || skippedSet[node.ID] || len(incoming[node.ID]) == 0 {
				continue
			}
			allResolved, anyChosen := true, false
			for _, edge := range incoming[node.ID] {
				resolved, chosen := selected(edge)
				if skippedSet[edge.From] {
					resolved, chosen = true, false
				}
				allResolved = allResolved && resolved
				anyChosen = anyChosen || chosen
			}
			if allResolved && !anyChosen {
				skippedSet[node.ID] = true
				changed = true
			}
		}
	}

	kernelNodes := make([]dag.Node, 0, len(spec.Nodes)-len(skippedSet))
	statuses := make(map[string]dag.Status, len(latest))
	byID := make(map[string]domain.Node, len(spec.Nodes))
	for _, node := range spec.Nodes {
		byID[node.ID] = node
		if skippedSet[node.ID] {
			continue
		}
		kernelNode := dag.Node{ID: node.ID}
		for _, edge := range incoming[node.ID] {
			resolved, chosen := selected(edge)
			if skippedSet[edge.From] {
				continue
			}
			if !resolved || chosen {
				kernelNode.DependsOn = append(kernelNode.DependsOn, edge.From)
			}
		}
		kernelNodes = append(kernelNodes, kernelNode)
		if attempt, exists := latest[node.ID]; exists {
			statuses[node.ID] = schedulerStatus(attempt.Status)
		}
	}
	readyIDs, _, _, err := dag.Ready(dag.Snapshot{Nodes: kernelNodes, Statuses: statuses})
	if err != nil {
		return nil, sortedKeys(skippedSet)
	}
	ready := make([]domain.Node, 0, len(readyIDs))
	for _, id := range readyIDs {
		if node, exists := byID[id]; exists {
			ready = append(ready, node)
		}
	}
	return ready, sortedKeys(skippedSet)
}

func schedulerStatus(status domain.AttemptStatus) dag.Status {
	switch status {
	case domain.AttemptStatusSucceeded:
		return dag.StatusSucceeded
	case domain.AttemptStatusFailed, domain.AttemptStatusManualIntervention:
		return dag.StatusFailed
	case domain.AttemptStatusCanceled:
		return dag.StatusCancelled
	case domain.AttemptStatusSkipped:
		return dag.StatusSucceeded
	default:
		return dag.StatusRunning
	}
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
