package graph_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/stretchr/testify/require"
)

type counter struct{ N int }

func inc(_ context.Context, s counter) (counter, error)  { s.N++; return s, nil }
func boom(_ context.Context, s counter) (counter, error) { return s, errors.New("boom") }

func TestStateGraph_HappyPath(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddEdges("inc", graph.END)
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, 1, out.N)
}

func TestStateGraph_ConditionalEdge(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddConditionalEdge("inc", func(s counter) []string {
		if s.N < 3 {
			return []string{"inc"}
		}
		return []string{graph.END}
	})
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, 3, out.N)
}

func TestStateGraph_MaxSteps(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddEdges("inc", "inc")
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 3})
	require.Error(t, err)
	require.Contains(t, err.Error(), "max steps")
}

func TestStateGraph_NodeError(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("boom", boom)
	g.AddEdges("boom", graph.END)
	g.SetEntryPoint("boom")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.ErrorContains(t, err, "boom")
}

func TestStateGraph_PanicRecovery(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("panic", func(_ context.Context, s counter) (counter, error) { panic("oh no") })
	g.AddEdges("panic", graph.END)
	g.SetEntryPoint("panic")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.ErrorContains(t, err, "panic")
}

func TestStateGraph_CompileErrors(t *testing.T) {
	_, err := graph.New[counter]().Compile()
	require.ErrorContains(t, err, "entry point")

	g2 := graph.New[counter]()
	g2.SetEntryPoint("missing")
	_, err = g2.Compile()
	require.ErrorContains(t, err, "not registered")
}

// TestStateGraph_FanOutAndJoin verifies wave semantics: fan-out nodes run in
// one wave, the join runs only after every activated source executed, and
// each node runs exactly once.
func TestStateGraph_FanOutAndJoin(t *testing.T) {
	var orderMu sync.Mutex
	var order []string
	record := func(name string) func(context.Context, counter) (counter, error) {
		return func(_ context.Context, s counter) (counter, error) {
			orderMu.Lock()
			order = append(order, name)
			orderMu.Unlock()
			s.N++
			return s, nil
		}
	}
	g := graph.New[counter]()
	g.AddNode("a", record("a"))
	g.AddNode("b", record("b"))
	g.AddNode("c", record("c"))
	g.AddNode("join", record("join"))
	g.AddEdges("a", "b", "c") // fan-out
	g.AddEdges("b", "join")
	g.AddEdges("c", "join") // fan-in
	g.AddEdges("join", graph.END)
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	// Parallel fan-out nodes all start from the same wave state; with the
	// default last-write-wins merge only the final successor's increment
	// survives, so join sees N=2 and yields 3 overall.
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, 3, out.N) // a + one fan-out + join
	require.Equal(t, []string{"a", "b", "c", "join"}, order)
}

// TestStateGraph_Deadlock covers a join whose activated sources block each
// other through a static-edge cycle: pending stays non-empty but no node is
// ever ready.
func TestStateGraph_Deadlock(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("a", inc)
	g.AddNode("b", inc)
	g.AddNode("d", inc)
	g.AddConditionalEdge("a", func(counter) []string { return []string{"b", "d"} })
	g.AddEdges("d", "b") // b is blocked on d
	g.AddEdges("b", "d") // d is blocked on b
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 10})
	require.Error(t, err)
	require.Contains(t, err.Error(), "deadlock")
}

// TestStateGraph_MaxParallel caps concurrent nodes per wave. Four ready nodes
// with MaxParallel=2 must never observe more than two in flight.
func TestStateGraph_MaxParallel(t *testing.T) {
	var inFlight, peak atomic.Int32
	g := graph.New[counter]()
	g.AddNode("a", inc)
	for _, name := range []string{"b", "c", "d", "e"} {
		g.AddNode(name, func(_ context.Context, s counter) (counter, error) {
			cur := inFlight.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			s.N++
			return s, nil
		})
		g.AddEdges(name, graph.END)
	}
	g.AddConditionalEdge("a", func(counter) []string { return []string{"b", "c", "d", "e"} })
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{
		MaxSteps:    10,
		MaxParallel: 2,
		MergeWave: func(base counter, results []graph.WaveResult[counter]) (counter, error) {
			// Each node increments exactly once; sum the wave deltas.
			base.N += len(results)
			return base, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 5, out.N)
	require.Equal(t, int32(2), peak.Load())
}

// TestStateGraph_MergeWave folds per-node results through the supplied merge
// function; here the wave sums partial counters instead of last-write-wins.
func TestStateGraph_MergeWave(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("a", inc)
	g.AddNode("b", inc)
	g.AddNode("c", inc)
	g.AddEdges("a", "b", "c")
	g.AddEdges("b", graph.END)
	g.AddEdges("c", graph.END)
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{
		MaxSteps: 10,
		MergeWave: func(base counter, results []graph.WaveResult[counter]) (counter, error) {
			// Each node increments exactly once; sum the wave deltas.
			base.N += len(results)
			return base, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 3, out.N) // a=1 + wave[b,c]=2
}

// TestStateGraph_AfterStepPerWave asserts AfterStep fires once per wave, not
// once per node: waves are [a], [b, c], [join].
func TestStateGraph_AfterStepPerWave(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("a", inc)
	g.AddNode("b", inc)
	g.AddNode("c", inc)
	g.AddNode("join", inc)
	g.AddEdges("a", "b", "c")
	g.AddEdges("b", "join")
	g.AddEdges("c", "join")
	g.AddEdges("join", graph.END)
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	var afterWaves atomic.Int32
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{
		MaxSteps: 10,
		AfterStep: func(_ context.Context, s counter) error {
			afterWaves.Add(1)
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 3, out.N) // last-write-wins: a + one fan-out + join
	require.Equal(t, int32(3), afterWaves.Load())
}

func TestStateGraph_RouteErrors(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("a", inc)
	g.AddConditionalEdge("a", func(counter) []string { return []string{"missing"} })
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.ErrorContains(t, err, "unregistered node")

	g2 := graph.New[counter]()
	g2.AddNode("a", inc)
	g2.AddConditionalEdge("a", func(counter) []string { return nil })
	g2.SetEntryPoint("a")
	cg2, err := g2.Compile()
	require.NoError(t, err)
	_, err = cg2.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.ErrorContains(t, err, "returned no target")

	g3 := graph.New[counter]()
	g3.AddNode("a", inc)
	g3.SetEntryPoint("a")
	cg3, err := g3.Compile()
	require.NoError(t, err)
	_, err = cg3.Invoke(context.Background(), counter{}, graph.RunConfig[counter]{MaxSteps: 5})
	require.ErrorContains(t, err, "no outgoing edge")
}

// TestStateGraph_Cancellation stops the wave loop when the context is done.
func TestStateGraph_Cancellation(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("a", inc)
	g.AddEdges("a", "a")
	g.SetEntryPoint("a")
	cg, err := g.Compile()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = cg.Invoke(ctx, counter{}, graph.RunConfig[counter]{MaxSteps: 10})
	require.ErrorIs(t, err, context.Canceled)
}
