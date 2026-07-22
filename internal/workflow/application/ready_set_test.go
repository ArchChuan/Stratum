package application_test

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

func TestReadySetFansOutAndJoinsOnlyAfterAllSelectedParentsSucceed(t *testing.T) {
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "root", Type: domain.NodeTypeAgent, AgentID: "a"},
			{ID: "left", Type: domain.NodeTypeAgent, AgentID: "b"},
			{ID: "right", Type: domain.NodeTypeAgent, AgentID: "c"},
			{ID: "join", Type: domain.NodeTypeAgent, AgentID: "d"},
		},
		Edges: []domain.Edge{{ID: "rl", From: "root", To: "left"}, {ID: "rr", From: "root", To: "right"}, {ID: "lj", From: "left", To: "join"}, {ID: "rj", From: "right", To: "join"}},
	}

	ready, skipped := application.ReadySet(spec, nil)
	require.Equal(t, []string{"root"}, nodeIDs(ready))
	require.Empty(t, skipped)

	ready, _ = application.ReadySet(spec, []domain.NodeAttempt{{NodeID: "root", Status: domain.AttemptStatusSucceeded}})
	require.ElementsMatch(t, []string{"left", "right"}, nodeIDs(ready))

	ready, _ = application.ReadySet(spec, []domain.NodeAttempt{{NodeID: "root", Status: domain.AttemptStatusSucceeded}, {NodeID: "left", Status: domain.AttemptStatusSucceeded}})
	require.Equal(t, []string{"right"}, nodeIDs(ready))
}

func TestReadySetSkipsUnselectedConditionBranchAndPropagates(t *testing.T) {
	trueValue := true
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "condition", Type: domain.NodeTypeCondition, Condition: `$.ok == true`},
			{ID: "yes", Type: domain.NodeTypeAgent, AgentID: "a"},
			{ID: "no", Type: domain.NodeTypeAgent, AgentID: "b"},
			{ID: "yes_child", Type: domain.NodeTypeAgent, AgentID: "c"},
		},
		Edges: []domain.Edge{
			{ID: "yes", From: "condition", To: "yes", ConditionValue: &trueValue},
			{ID: "no", From: "condition", To: "no", Default: true},
			{ID: "yes-child", From: "yes", To: "yes_child"},
		},
	}
	attempts := []domain.NodeAttempt{{NodeID: "condition", Status: domain.AttemptStatusSucceeded, SelectedEdges: []string{"no"}}}
	ready, skipped := application.ReadySet(spec, attempts)
	require.Equal(t, []string{"no"}, nodeIDs(ready))
	require.ElementsMatch(t, []string{"yes", "yes_child"}, skipped)
}

func TestReadySetReturnsStableOrderForUnorderedDefinition(t *testing.T) {
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "zulu", Type: domain.NodeTypeAgent, AgentID: "z"},
			{ID: "alpha", Type: domain.NodeTypeAgent, AgentID: "a"},
			{ID: "middle", Type: domain.NodeTypeAgent, AgentID: "m"},
		},
	}

	ready, skipped := application.ReadySet(spec, nil)
	require.Equal(t, []string{"alpha", "middle", "zulu"}, nodeIDs(ready))
	require.Empty(t, skipped)
}

func TestReadySetDoesNotReleaseChildAfterParentFailure(t *testing.T) {
	spec := domain.Spec{
		Nodes: []domain.Node{
			{ID: "parent", Type: domain.NodeTypeAgent, AgentID: "a"},
			{ID: "child", Type: domain.NodeTypeAgent, AgentID: "b"},
		},
		Edges: []domain.Edge{{From: "parent", To: "child"}},
	}

	ready, skipped := application.ReadySet(spec, []domain.NodeAttempt{{
		NodeID: "parent",
		Status: domain.AttemptStatusFailed,
	}})
	require.Empty(t, ready)
	require.Empty(t, skipped)
}

func nodeIDs(nodes []domain.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}
