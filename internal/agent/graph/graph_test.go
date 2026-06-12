package graph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/graph"
	"github.com/stretchr/testify/require"
)

type counter struct{ N int }

func inc(_ context.Context, s counter) (counter, error)  { s.N++; return s, nil }
func boom(_ context.Context, s counter) (counter, error) { return s, errors.New("boom") }

func TestStateGraph_HappyPath(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddEdge("inc", graph.END)
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	require.Equal(t, 1, out.N)
}

func TestStateGraph_ConditionalEdge(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddConditionalEdge("inc", func(s counter) string {
		if s.N < 3 {
			return "inc"
		}
		return graph.END
	})
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	out, err := cg.Invoke(context.Background(), counter{}, graph.RunConfig{MaxSteps: 10})
	require.NoError(t, err)
	require.Equal(t, 3, out.N)
}

func TestStateGraph_MaxSteps(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("inc", inc)
	g.AddEdge("inc", "inc")
	g.SetEntryPoint("inc")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig{MaxSteps: 3})
	require.Error(t, err)
	require.Contains(t, err.Error(), "max steps")
}

func TestStateGraph_NodeError(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("boom", boom)
	g.AddEdge("boom", graph.END)
	g.SetEntryPoint("boom")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig{MaxSteps: 5})
	require.ErrorContains(t, err, "boom")
}

func TestStateGraph_PanicRecovery(t *testing.T) {
	g := graph.New[counter]()
	g.AddNode("panic", func(_ context.Context, s counter) (counter, error) { panic("oh no") })
	g.AddEdge("panic", graph.END)
	g.SetEntryPoint("panic")
	cg, err := g.Compile()
	require.NoError(t, err)
	_, err = cg.Invoke(context.Background(), counter{}, graph.RunConfig{MaxSteps: 5})
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
