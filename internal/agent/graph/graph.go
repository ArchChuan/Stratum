package graph

import (
	"context"
	"fmt"
	"runtime/debug"
)

// END is the terminal node name; routing to it stops the graph.
const END = "__end__"

// NodeFunc is the function signature for a graph node.
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)

// EdgeFunc decides the next node name from the current state.
type EdgeFunc[S any] func(state S) string

// StateGraph is a generic, directed graph of stateful nodes.
type StateGraph[S any] struct {
	nodes     map[string]NodeFunc[S]
	edges     map[string]string
	condEdges map[string]EdgeFunc[S]
	entry     string
}

// New creates an empty StateGraph.
func New[S any]() *StateGraph[S] {
	return &StateGraph[S]{
		nodes:     make(map[string]NodeFunc[S]),
		edges:     make(map[string]string),
		condEdges: make(map[string]EdgeFunc[S]),
	}
}

// AddNode registers a node.
func (g *StateGraph[S]) AddNode(name string, fn NodeFunc[S]) *StateGraph[S] {
	g.nodes[name] = fn
	return g
}

// AddEdge adds a static edge from → to.
func (g *StateGraph[S]) AddEdge(from, to string) *StateGraph[S] {
	g.edges[from] = to
	return g
}

// AddConditionalEdge adds a dynamic edge; fn returns the next node name.
// Conditional edges take precedence over static edges.
func (g *StateGraph[S]) AddConditionalEdge(from string, fn EdgeFunc[S]) *StateGraph[S] {
	g.condEdges[from] = fn
	return g
}

// SetEntryPoint sets the starting node.
func (g *StateGraph[S]) SetEntryPoint(name string) *StateGraph[S] {
	g.entry = name
	return g
}

// CompiledGraph is a validated, runnable graph.
type CompiledGraph[S any] struct{ g *StateGraph[S] }

// RunConfig controls execution behaviour.
type RunConfig struct {
	MaxSteps int
}

// Compile validates the graph and returns a runnable CompiledGraph.
func (g *StateGraph[S]) Compile() (*CompiledGraph[S], error) {
	if g.entry == "" {
		return nil, fmt.Errorf("graph: entry point not set")
	}
	if _, ok := g.nodes[g.entry]; !ok {
		return nil, fmt.Errorf("graph: entry node %q not registered", g.entry)
	}
	for from, to := range g.edges {
		if to != END {
			if _, ok := g.nodes[to]; !ok {
				return nil, fmt.Errorf("graph: edge %q → %q: target node not registered", from, to)
			}
		}
	}
	return &CompiledGraph[S]{g: g}, nil
}

// Invoke runs the graph from the entry node until END or max steps.
func (c *CompiledGraph[S]) Invoke(ctx context.Context, initial S, cfg RunConfig) (state S, _ error) {
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10
	}
	state = initial
	current := c.g.entry
	for step := 0; step < maxSteps; step++ {
		if current == END {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		default:
		}
		nodeFn, ok := c.g.nodes[current]
		if !ok {
			return state, fmt.Errorf("graph: node %q not found", current)
		}
		var execErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					execErr = fmt.Errorf("graph: node %q panic: %v\n%s", current, r, debug.Stack())
				}
			}()
			state, execErr = nodeFn(ctx, state)
		}()
		if execErr != nil {
			return state, execErr
		}
		if condFn, ok := c.g.condEdges[current]; ok {
			current = condFn(state)
		} else if next, ok := c.g.edges[current]; ok {
			current = next
		} else {
			return state, fmt.Errorf("graph: no outgoing edge from node %q", current)
		}
	}
	return state, fmt.Errorf("graph: max steps reached: %d", maxSteps)
}
