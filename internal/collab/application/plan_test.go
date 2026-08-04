package application_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/collab/application"
	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
)

// idGen yields monotonic step ids for plan generation assertions.
func idGen() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("step-%d", n)
	}
}

// TestStartGeneratesDependencyStructure drives generateSteps through the
// service Start path and asserts the DAG shape per strategy.
func TestStartGeneratesDependencyStructure(t *testing.T) {
	tests := []struct {
		strategy    domain.CollabStrategy
		wantChained bool
	}{
		{domain.CollabSequential, true},
		{domain.CollabPipeline, true},
		{domain.CollabHierarchical, true},
		{domain.CollabParallel, false},
		{domain.CollabSwarm, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.strategy), func(t *testing.T) {
			store := newCollabStore()
			createdPlan(store, "plan-1", "tenant-1", "creator")
			store.collabs["plan-1"].Strategy = tc.strategy
			store.collabs["plan-1"].Participants = []string{"agent-1", "agent-2", "agent-3"}
			store.collabs["plan-1"].TaskDescription = "build the report"
			svc := application.NewCollaborationService(store, store.stepsRepo(), observability.NoopMetrics{}, idGen())

			collab, err := svc.Start(context.Background(), "tenant-1", "plan-1", fixedActor("admin", "admin-1"))
			require.NoError(t, err)
			require.Equal(t, domain.CollabRunning, collab.Status)
			steps := store.insertedSteps
			require.Len(t, steps, 3)
			for i, step := range steps {
				require.Equal(t, "plan-1", step.PlanID)
				require.Equal(t, fmt.Sprintf("agent-%d", i+1), step.AgentID)
				require.Equal(t, domain.TaskPending, step.Status)
				require.Equal(t, "no_delegate", step.Delegation)
				require.Equal(t, 3, step.MaxRetries)
				require.Equal(t, int64(0), step.Generation)
				require.Equal(t, i, step.Input["step_index"])
				require.Equal(t, 3, step.Input["total_steps"])
				require.Equal(t, "build the report", step.Input["query"])
				if tc.wantChained {
					if i == 0 {
						require.Empty(t, step.Dependencies)
					} else {
						require.Equal(t, []string{steps[i-1].ID}, step.Dependencies)
					}
				} else {
					require.Empty(t, step.Dependencies)
				}
			}
		})
	}
}
