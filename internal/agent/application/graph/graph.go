package graph

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
)

// END is the terminal node name; routing to it stops that path of the graph.
const END = "__end__"

// DefaultMaxSteps bounds wave count when RunConfig.MaxSteps is unset (≤0).
const DefaultMaxSteps = 10

// NodeFunc is the function signature for a graph node.
type NodeFunc[S any] func(ctx context.Context, state S) (S, error)

// EdgeFunc decides the next node names from the current state. Conditional
// edges take precedence over static edges; returning an empty slice is an
// error (dead end).
type EdgeFunc[S any] func(state S) []string

// WaveResult is the outcome of one node in one wave.
type WaveResult[S any] struct {
	Node  string
	State S
	Err   error
}

// StateGraph is a generic, directed graph of stateful nodes. Static edges
// fan out to multiple targets; fan-in (join) is expressed by several nodes
// sharing a common successor, which runs only after all activated sources
// have executed.
type StateGraph[S any] struct {
	nodes     map[string]NodeFunc[S]
	edges     map[string][]string
	condEdges map[string]EdgeFunc[S]
	incoming  map[string][]string
	entry     string
}

// New creates an empty StateGraph.
func New[S any]() *StateGraph[S] {
	return &StateGraph[S]{
		nodes:     make(map[string]NodeFunc[S]),
		edges:     make(map[string][]string),
		condEdges: make(map[string]EdgeFunc[S]),
		incoming:  make(map[string][]string),
	}
}

// AddNode registers a node.
func (g *StateGraph[S]) AddNode(name string, fn NodeFunc[S]) *StateGraph[S] {
	g.nodes[name] = fn
	return g
}

// AddEdges adds static edges from → to... (fan-out). Each target must be a
// registered node or END. A node with no outgoing edge of any kind is a
// compile-time error; callers without a successor must add one.
func (g *StateGraph[S]) AddEdges(from string, to ...string) *StateGraph[S] {
	if len(to) == 0 {
		return g
	}
	g.edges[from] = to
	for _, t := range to {
		if t != END {
			g.incoming[t] = append(g.incoming[t], from)
		}
	}
	return g
}

// AddConditionalEdge adds a dynamic edge; fn returns the next node names.
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

// RunConfig controls execution behaviour. MaxSteps is the wave budget (each
// wave may run several nodes concurrently); MaxParallel caps in-wave
// concurrency (≤0 = sequential); AfterStep runs once per wave after merge;
// MergeWave folds per-node results back into the running state.
type RunConfig[S any] struct {
	MaxSteps    int
	MaxParallel int
	AfterStep   func(ctx context.Context, state S) error
	MergeWave   func(base S, results []WaveResult[S]) (S, error)
}

// Compile validates the graph and returns a runnable CompiledGraph.
func (g *StateGraph[S]) Compile() (*CompiledGraph[S], error) {
	if g.entry == "" {
		return nil, fmt.Errorf("graph: entry point not set")
	}
	if _, ok := g.nodes[g.entry]; !ok {
		return nil, fmt.Errorf("graph: entry node %q not registered", g.entry)
	}
	for from, targets := range g.edges {
		for _, t := range targets {
			if t == END {
				continue
			}
			if _, ok := g.nodes[t]; !ok {
				return nil, fmt.Errorf("graph: edge %q → %q: target node not registered", from, t)
			}
		}
	}
	return &CompiledGraph[S]{g: g}, nil
}

// Invoke runs the graph in waves until every path reaches END or the wave
// budget is exhausted. A wave executes all ready pending nodes (possibly
// concurrently up to cfg.MaxParallel), then routes each successful node and
// merges results; cfg.AfterStep runs once per wave.
func (c *CompiledGraph[S]) Invoke(ctx context.Context, initial S, cfg RunConfig[S]) (S, error) {
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	state := initial
	pending := map[string]struct{}{c.g.entry: {}}
	activated := map[string]struct{}{c.g.entry: {}}
	executed := map[string]struct{}{}
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		ready := c.readyNodes(pending, activated, executed)
		if len(ready) == 0 {
			if len(pending) == 0 {
				return state, nil
			}
			return state, fmt.Errorf("graph: deadlock: pending nodes never become ready: %v", pendingKeys(pending))
		}
		results := runWave(ctx, c.g.nodes, ready, state, cfg.MaxParallel)
		// Merge first, including failed nodes' partial state (observations,
		// trace events), so callers see what ran even on error. MergeWave
		// implementations fold WaveResult.State and ignore Err.
		merged, mergeErr := mergeWave(state, results, cfg.MergeWave)
		state = merged
		if err := waveErrors(results, mergeErr); err != nil {
			return state, err
		}
		// AfterStep runs once per wave on the merged state, including the
		// terminating wave: checkpointing must observe the final state.
		if cfg.AfterStep != nil {
			if err := cfg.AfterStep(ctx, state); err != nil {
				return state, err
			}
		}
		next, err := c.routeWave(results, state, executed, activated)
		if err != nil {
			return state, err
		}
		if len(next) == 0 {
			return state, nil
		}
		pending = next
	}
	return state, fmt.Errorf("graph: max steps (%d) exceeded", maxSteps)
}

// waveErrors returns the first node error, falling back to the merge error.
func waveErrors[S any](results []WaveResult[S], mergeErr error) error {
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return mergeErr
}

// routeWave marks successful nodes executed and appends their next targets to
// the pending set of the following wave.
func (c *CompiledGraph[S]) routeWave(results []WaveResult[S], state S, executed, activated map[string]struct{}) (map[string]struct{}, error) {
	next := make(map[string]struct{})
	for _, r := range results {
		executed[r.Node] = struct{}{}
		targets, err := c.route(r.Node, state)
		if err != nil {
			return nil, err
		}
		for _, t := range targets {
			if t == END {
				continue
			}
			if _, ok := c.g.nodes[t]; !ok {
				return nil, fmt.Errorf("graph: edge from %q targets unregistered node %q", r.Node, t)
			}
			next[t] = struct{}{}
			activated[t] = struct{}{}
		}
	}
	return next, nil
}

// readyNodes returns the sorted set of pending nodes whose activated static
// predecessors have all executed. A predecessor that was never activated
// (e.g. a fan-in source not scheduled this run) does not block the join.
func (c *CompiledGraph[S]) readyNodes(pending, activated, executed map[string]struct{}) []string {
	ready := make([]string, 0, len(pending))
	for v := range pending {
		blocked := false
		for _, u := range c.g.incoming[v] {
			if u == v {
				continue // self-loop never blocks
			}
			if _, wasActivated := activated[u]; wasActivated {
				if _, done := executed[u]; !done {
					blocked = true
					break
				}
			}
		}
		if !blocked {
			ready = append(ready, v)
		}
	}
	sort.Strings(ready)
	return ready
}

// route resolves the next targets for a successfully executed node:
// conditional edge first, then static edges.
func (c *CompiledGraph[S]) route(node string, state S) ([]string, error) {
	if fn, ok := c.g.condEdges[node]; ok {
		targets := fn(state)
		if len(targets) == 0 {
			return nil, fmt.Errorf("graph: conditional edge from %q returned no target", node)
		}
		return targets, nil
	}
	if targets, ok := c.g.edges[node]; ok {
		return targets, nil
	}
	return nil, fmt.Errorf("graph: no outgoing edge from node %q", node)
}

// runWave executes the ready nodes of one wave, capped at maxParallel
// concurrent goroutines (≤0 = sequential). Results preserve the sorted ready
// order for deterministic merging.
func runWave[S any](ctx context.Context, nodes map[string]NodeFunc[S], ready []string, state S, maxParallel int) []WaveResult[S] {
	results := make([]WaveResult[S], len(ready))
	if maxParallel > 0 {
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for i, name := range ready {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					results[i] = WaveResult[S]{Node: name, Err: ctx.Err()}
					return
				}
				defer func() { <-sem }()
				results[i] = execNode(nodes[name], name, ctx, state)
			}(i, name)
		}
		wg.Wait()
		return results
	}
	for i, name := range ready {
		results[i] = execNode(nodes[name], name, ctx, state)
	}
	return results
}

// execNode runs one node with panic recovery, mirroring the serial executor's
// error contract.
func execNode[S any](fn NodeFunc[S], name string, ctx context.Context, state S) WaveResult[S] {
	var s S
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("graph: node %q panic: %v\n%s", name, r, debug.Stack())
			}
		}()
		s, err = fn(ctx, state)
	}()
	return WaveResult[S]{Node: name, State: s, Err: err}
}

// mergeWave folds wave results into the running state. Without a custom
// MergeWave, later results in sorted node order win (last-write-wins).
func mergeWave[S any](base S, results []WaveResult[S], merge func(base S, results []WaveResult[S]) (S, error)) (S, error) {
	if merge == nil {
		return results[len(results)-1].State, nil
	}
	return merge(base, results)
}

func pendingKeys(pending map[string]struct{}) []string {
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
