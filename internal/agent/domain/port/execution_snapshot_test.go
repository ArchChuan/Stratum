package port

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionSnapshotCtx(t *testing.T) {
	require.Nil(t, ExecutionSnapshotFromCtx(context.Background()))
	es := &ExecutionSnapshot{
		TraceParameters:     map[string]any{"trace.capture_parameters": true},
		ContextWindowTokens: 8000,
		OutputReserveTokens: 4096,
		PinnedMCP:           map[string]MCPRevisionPin{"mcp-a": {RevisionID: "mcp-rev-1"}},
		PinnedKnowledge:     map[string]KnowledgeRevisionPin{"wiki": {RevisionID: "kb-rev-2"}},
	}
	ctx := WithExecutionSnapshot(context.Background(), es)
	require.Same(t, es, ExecutionSnapshotFromCtx(ctx))
	require.Nil(t, ExecutionSnapshotFromCtx(context.Background()))
}

func TestMCPRevisionPin(t *testing.T) {
	pin := MCPRevisionPin{RevisionID: "r1", ExperimentID: "exp", Variant: "canary"}
	require.Equal(t, "r1", pin.RevisionID)
}
