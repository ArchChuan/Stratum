package domain

import (
	"errors"
	"testing"
)

func TestRemovePlanNode(t *testing.T) {
	// node-2 依赖 node-1，叶子节点可删。
	plan := activePlan()
	if err := removePlanNode(plan, "node-2"); err != nil {
		t.Fatalf("remove leaf: %v", err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].ID != "node-1" {
		t.Errorf("node-2 not removed: %+v", plan.Nodes)
	}
}

func TestRemovePlanNodeRejectsDependentNode(t *testing.T) {
	// 极端情况：仍被依赖的节点（node-1 被 node-2 依赖）不可删除。
	plan := activePlan()
	if err := removePlanNode(plan, "node-1"); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("expected ErrInvalidPlan for depended-on node, got %v", err)
	}
}

func TestRemovePlanNodeMissingNode(t *testing.T) {
	// 极端情况：不存在的节点返回错误。
	plan := activePlan()
	if err := removePlanNode(plan, "ghost"); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("expected ErrInvalidPlan, got %v", err)
	}
}

func TestTerminalNodeStatus(t *testing.T) {
	terminal := []PlanNodeStatus{PlanNodeStatusSucceeded, PlanNodeStatusFailed, PlanNodeStatusBlocked, PlanNodeStatusCancelled, PlanNodeStatusFailedPendingConfirmation}
	for _, s := range terminal {
		if !terminalNodeStatus(s) {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []PlanNodeStatus{PlanNodeStatusPending, PlanNodeStatusRunning, PlanNodeStatus("")} {
		if terminalNodeStatus(s) {
			t.Errorf("%s must not be terminal", s)
		}
	}
}

func TestTerminalPlanStatus(t *testing.T) {
	for _, s := range []PlanStatus{PlanStatusCompleted, PlanStatusFailed, PlanStatusCancelled} {
		if !terminalPlanStatus(s) {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []PlanStatus{PlanStatusActive, PlanStatusRevising, PlanStatus("")} {
		if terminalPlanStatus(s) {
			t.Errorf("%s must not be terminal", s)
		}
	}
}

func TestClonePlanDeepCopiesSlices(t *testing.T) {
	plan := activePlan()
	plan.Nodes[0].DependsOn = []string{"x"}
	plan.Nodes[0].HintTools = []string{"tool-a"}
	plan.Nodes[0].Attempts = []PlanAttempt{{ID: "a1"}}

	clone := clonePlan(plan)
	clone.Nodes[0].DependsOn[0] = "mutated"
	clone.Nodes[0].HintTools[0] = "mutated"
	clone.Nodes[0].Attempts[0].ID = "mutated"

	if plan.Nodes[0].DependsOn[0] != "x" {
		t.Error("DependsOn must be deep-copied")
	}
	if plan.Nodes[0].HintTools[0] != "tool-a" {
		t.Error("HintTools must be deep-copied")
	}
	if plan.Nodes[0].Attempts[0].ID != "a1" {
		t.Error("Attempts must be deep-copied")
	}
	if clone.Revision != plan.Revision {
		t.Error("scalar fields must be copied")
	}
}
