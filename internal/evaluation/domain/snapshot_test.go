package domain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvalSnapshotRoundTrip(t *testing.T) {
	snap := &EvaluationContextSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Evaluation: GroupSnapshot{GroupKey: GroupEvaluation, VersionSeq: 3,
			Values: map[string]any{"evaluation.judge.enabled": true, "evaluation.observe.sample_rate": 0.5}},
		Execution: []GroupSnapshot{
			{GroupKey: GroupAgent, VersionSeq: 2, Values: map[string]any{}},
			{GroupKey: GroupTrace, VersionSeq: 1, Values: map[string]any{"trace.capture_parameters": false}},
		},
		ResolvedExecution: ResolvedExecution{ContextWindow: 8000, OutputReserve: 4096},
		PinnedAssignments: PinnedAssignments{
			SkillAgentRevision: map[string]string{"skill-1": "rev-9"},
			MCPRevisions:       map[string]string{"mcp-a": "mcp-rev-1"},
			KnowledgeRevisions: map[string]string{"wiki": "kb-rev-2"},
		},
		CapturedAt: time.Now().UTC(),
		CapturedBy: "user-1",
	}
	data, err := json.Marshal(snap)
	require.NoError(t, err)
	var got EvaluationContextSnapshot
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, snap.SchemaVersion, got.SchemaVersion)
	require.Equal(t, snap.Evaluation.VersionSeq, got.Evaluation.VersionSeq)
	require.Equal(t, snap.ResolvedExecution, got.ResolvedExecution)
	require.Equal(t, snap.PinnedAssignments, got.PinnedAssignments)
	require.Len(t, got.Execution, 2)
}

func TestEvalSnapshotCtx(t *testing.T) {
	require.Nil(t, EvalSnapshotFromCtx(context.Background()))
	snap := &EvaluationContextSnapshot{SchemaVersion: 1}
	ctx := WithEvalSnapshot(context.Background(), snap)
	require.Same(t, snap, EvalSnapshotFromCtx(ctx))
	require.Nil(t, EvalSnapshotFromCtx(context.WithValue(context.Background(), otherCtxKey{}, 1)))
}

// otherCtxKey 是测试专用的无关 ctx key（避免使用内置类型 string 触发 SA1029）。
type otherCtxKey struct{}
