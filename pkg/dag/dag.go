// Package dag provides domain-neutral dependency validation and scheduling.
package dag

import (
	"fmt"
	"sort"
)

// Status is the scheduler-visible lifecycle state of a node.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusBlocked   Status = "blocked"
	StatusCancelled Status = "cancelled"
)

// Node declares a node and the nodes that must succeed before it is ready.
type Node struct {
	ID        string
	DependsOn []string
}

// Snapshot is the immutable graph plus its current node statuses.
// Missing statuses are treated as pending.
type Snapshot struct {
	Nodes    []Node
	Statuses map[string]Status
}

// Validate rejects malformed graphs and dependency cycles.
func Validate(nodes []Node) error {
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		if node.ID == "" {
			return fmt.Errorf("dag: node ID is required")
		}
		if _, exists := byID[node.ID]; exists {
			return fmt.Errorf("dag: duplicate node %q", node.ID)
		}
		byID[node.ID] = node
	}

	indegree := make(map[string]int, len(nodes))
	outgoing := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		seen := make(map[string]struct{}, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			if dependency == node.ID {
				return fmt.Errorf("dag: node %q depends on itself", node.ID)
			}
			if _, exists := byID[dependency]; !exists {
				return fmt.Errorf("dag: node %q depends on missing node %q", node.ID, dependency)
			}
			if _, exists := seen[dependency]; exists {
				return fmt.Errorf("dag: node %q repeats dependency %q", node.ID, dependency)
			}
			seen[dependency] = struct{}{}
			indegree[node.ID]++
			outgoing[dependency] = append(outgoing[dependency], node.ID)
		}
	}

	queue := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if indegree[node.ID] == 0 {
			queue = append(queue, node.ID)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, child := range outgoing[id] {
			indegree[child]--
			if indegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("dag: dependency cycle")
	}
	return nil
}

// Ready returns pending nodes whose dependencies succeeded and pending nodes
// made unreachable by a terminal dependency failure.
func Ready(snapshot Snapshot) (ready, blocked []string, complete bool, err error) {
	if err := Validate(snapshot.Nodes); err != nil {
		return nil, nil, false, err
	}
	byID := make(map[string]Node, len(snapshot.Nodes))
	statuses := make(map[string]Status, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		byID[node.ID] = node
		statuses[node.ID] = StatusPending
	}
	for id, status := range snapshot.Statuses {
		if _, exists := byID[id]; !exists {
			return nil, nil, false, fmt.Errorf("dag: status references missing node %q", id)
		}
		if !validStatus(status) {
			return nil, nil, false, fmt.Errorf("dag: node %q has unknown status %q", id, status)
		}
		statuses[id] = status
	}

	changed := true
	for changed {
		changed = false
		for _, node := range snapshot.Nodes {
			if statuses[node.ID] != StatusPending {
				continue
			}
			for _, dependency := range node.DependsOn {
				if preventsExecution(statuses[dependency]) {
					statuses[node.ID] = StatusBlocked
					changed = true
					break
				}
			}
		}
	}

	complete = true
	for _, node := range snapshot.Nodes {
		status := statuses[node.ID]
		if status == StatusPending {
			allSucceeded := true
			for _, dependency := range node.DependsOn {
				if statuses[dependency] != StatusSucceeded {
					allSucceeded = false
					break
				}
			}
			if allSucceeded {
				ready = append(ready, node.ID)
			}
		}
		if status == StatusBlocked && snapshot.Statuses[node.ID] != StatusBlocked {
			blocked = append(blocked, node.ID)
		}
		if !terminal(status) {
			complete = false
		}
	}
	sort.Strings(ready)
	sort.Strings(blocked)
	return ready, blocked, complete, nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusPending, StatusRunning, StatusSucceeded, StatusFailed, StatusBlocked, StatusCancelled:
		return true
	default:
		return false
	}
}

func preventsExecution(status Status) bool {
	return status == StatusFailed || status == StatusBlocked || status == StatusCancelled
}

func terminal(status Status) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusBlocked || status == StatusCancelled
}
