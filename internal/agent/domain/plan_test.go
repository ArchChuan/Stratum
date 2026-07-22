package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestApplyPlanCommandCreatesRuntimeOwnedDAG(t *testing.T) {
	ids := &sequenceIDs{values: []string{"plan-1", "node-1", "node-2", "node-3"}}
	plan, err := ApplyPlanCommand(nil, PlanCommand{
		Kind:             PlanCommandCreate,
		ExpectedRevision: 0,
		Nodes: []PlanNodeInput{
			{Key: "research", Goal: "Collect evidence", HintTools: []string{"search"}},
			{Key: "draft", Goal: "Draft answer", DependsOn: []string{"research"}},
			{Key: "review", Goal: "Review answer", DependsOn: []string{"draft"}},
		},
	}, ids.Next, testPlanLimits())

	if err != nil {
		t.Fatalf("ApplyPlanCommand() error = %v", err)
	}
	if plan.ID != "plan-1" || plan.Revision != 1 || plan.Status != PlanStatusActive {
		t.Fatalf("unexpected plan header: %+v", plan)
	}
	if got := nodeIDs(plan.Nodes); !reflect.DeepEqual(got, []string{"node-1", "node-2", "node-3"}) {
		t.Fatalf("node IDs = %v", got)
	}
	if got := plan.Nodes[1].DependsOn; !reflect.DeepEqual(got, []string{"node-1"}) {
		t.Fatalf("draft dependencies = %v", got)
	}
}

func TestApplyPlanCommandRejectsStaleRevisionWithoutMutation(t *testing.T) {
	plan := activePlan()
	before := clonePlanForTest(plan)

	_, err := ApplyPlanCommand(plan, PlanCommand{
		Kind:             PlanCommandRevise,
		ExpectedRevision: plan.Revision - 1,
		Operations:       []PlanRevisionOperation{{Kind: PlanOperationUpdate, NodeID: "node-1", Goal: "changed"}},
	}, (&sequenceIDs{}).Next, testPlanLimits())

	if !errors.Is(err, ErrPlanRevisionConflict) {
		t.Fatalf("error = %v, want ErrPlanRevisionConflict", err)
	}
	if !reflect.DeepEqual(plan, before) {
		t.Fatalf("input plan mutated: got %+v want %+v", plan, before)
	}
}

func TestApplyPlanCommandRejectsRevisionCycleWithoutMutation(t *testing.T) {
	plan := activePlan()
	before := clonePlanForTest(plan)

	_, err := ApplyPlanCommand(plan, PlanCommand{
		Kind:             PlanCommandRevise,
		ExpectedRevision: plan.Revision,
		Operations: []PlanRevisionOperation{
			{Kind: PlanOperationReplaceDependencies, NodeID: "node-1", DependsOn: []string{"node-2"}},
		},
	}, (&sequenceIDs{}).Next, testPlanLimits())

	if !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("error = %v, want ErrInvalidPlan", err)
	}
	if !reflect.DeepEqual(plan, before) {
		t.Fatalf("input plan mutated: got %+v want %+v", plan, before)
	}
}

func TestApplyPlanCommandRevisesNodesAtomically(t *testing.T) {
	plan := activePlan()
	ids := &sequenceIDs{values: []string{"node-3"}}

	revised, err := ApplyPlanCommand(plan, PlanCommand{
		Kind:             PlanCommandRevise,
		ExpectedRevision: 1,
		Operations: []PlanRevisionOperation{
			{Kind: PlanOperationUpdate, NodeID: "node-1", Goal: "updated"},
			{Kind: PlanOperationAdd, Key: "third", Goal: "third", DependsOn: []string{"node-2"}},
			{Kind: PlanOperationReplaceDependencies, NodeID: "node-2", DependsOn: []string{"node-1"}},
		},
	}, ids.Next, testPlanLimits())

	if err != nil {
		t.Fatalf("ApplyPlanCommand() error = %v", err)
	}
	if revised.Revision != 2 || len(revised.Nodes) != 3 {
		t.Fatalf("unexpected revised plan: %+v", revised)
	}
	if plan.Nodes[0].Goal == revised.Nodes[0].Goal {
		t.Fatal("revision mutated the original plan")
	}
}

func TestApplyPlanCommandEnforcesBudgetsAndTerminalState(t *testing.T) {
	limits := testPlanLimits()
	limits.MaxNodes = 1
	_, err := ApplyPlanCommand(nil, PlanCommand{
		Kind:  PlanCommandCreate,
		Nodes: []PlanNodeInput{{Key: "one", Goal: "one"}, {Key: "two", Goal: "two"}},
	}, (&sequenceIDs{values: []string{"plan-1"}}).Next, limits)
	if !errors.Is(err, ErrPlanBudgetExceeded) {
		t.Fatalf("node budget error = %v", err)
	}

	plan := activePlan()
	plan.Status = PlanStatusCompleted
	_, err = ApplyPlanCommand(plan, PlanCommand{
		Kind:             PlanCommandCancel,
		ExpectedRevision: plan.Revision,
	}, (&sequenceIDs{}).Next, testPlanLimits())
	if !errors.Is(err, ErrInvalidPlanTransition) {
		t.Fatalf("terminal mutation error = %v", err)
	}
}

func TestPlanNodeWithUncertainSideEffectCannotRetry(t *testing.T) {
	node := PlanNode{Status: PlanNodeStatusFailedPendingConfirmation}
	if node.CanRetry(3) {
		t.Fatal("CanRetry() = true for uncertain external side effect")
	}
	node.Status = PlanNodeStatusFailed
	node.Attempts = []PlanAttempt{{ID: "attempt-1"}}
	if !node.CanRetry(3) {
		t.Fatal("CanRetry() = false for ordinary failure below attempt budget")
	}
}

func activePlan() *Plan {
	return &Plan{
		ID:       "plan-1",
		Revision: 1,
		Status:   PlanStatusActive,
		Nodes: []PlanNode{
			{ID: "node-1", Goal: "one", Status: PlanNodeStatusPending},
			{ID: "node-2", Goal: "two", DependsOn: []string{"node-1"}, Status: PlanNodeStatusPending},
		},
	}
}

func testPlanLimits() PlanLimits {
	return PlanLimits{MaxNodes: 10, MaxRevisions: 10, MaxAttemptsPerNode: 3}
}

func nodeIDs(nodes []PlanNode) []string {
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node.ID)
	}
	return result
}

func clonePlanForTest(plan *Plan) *Plan {
	copyPlan := *plan
	copyPlan.Nodes = append([]PlanNode(nil), plan.Nodes...)
	for index := range copyPlan.Nodes {
		copyPlan.Nodes[index].DependsOn = append([]string(nil), plan.Nodes[index].DependsOn...)
		copyPlan.Nodes[index].HintTools = append([]string(nil), plan.Nodes[index].HintTools...)
		copyPlan.Nodes[index].Attempts = append([]PlanAttempt(nil), plan.Nodes[index].Attempts...)
	}
	return &copyPlan
}

type sequenceIDs struct {
	values []string
	index  int
}

func (s *sequenceIDs) Next() string {
	if s.index >= len(s.values) {
		s.index++
		return "generated"
	}
	value := s.values[s.index]
	s.index++
	return value
}
