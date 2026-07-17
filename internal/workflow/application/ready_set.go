package application

import (
	"sort"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
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
	outgoing := make(map[string][]domain.Edge)
	for _, edge := range spec.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge)
		outgoing[edge.From] = append(outgoing[edge.From], edge)
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

	ready := make([]domain.Node, 0)
	for _, node := range spec.Nodes {
		if _, exists := latest[node.ID]; exists || skippedSet[node.ID] {
			continue
		}
		edges := incoming[node.ID]
		if len(edges) == 0 {
			ready = append(ready, node)
			continue
		}
		allSucceeded, chosenCount := true, 0
		for _, edge := range edges {
			resolved, chosen := selected(edge)
			if !resolved {
				allSucceeded = false
				continue
			}
			if chosen {
				chosenCount++
				if latest[edge.From].Status != domain.AttemptStatusSucceeded {
					allSucceeded = false
				}
			}
		}
		if allSucceeded && chosenCount > 0 {
			ready = append(ready, node)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	skipped := make([]string, 0, len(skippedSet))
	for id := range skippedSet {
		skipped = append(skipped, id)
	}
	sort.Strings(skipped)
	_ = outgoing
	return ready, skipped
}
