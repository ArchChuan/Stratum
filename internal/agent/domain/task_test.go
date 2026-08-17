package domain

import (
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestBuildTaskSnapshot(t *testing.T) {
	succeeded := PlanNode{ID: "n1", Goal: "迁移订单服务", Status: PlanNodeStatusSucceeded}
	pending := PlanNode{ID: "n2", Goal: "验证迁移", DependsOn: []string{"n1"}, Status: PlanNodeStatusPending}
	failed := PlanNode{ID: "n3", Goal: "压测", Status: PlanNodeStatusFailed}
	cases := []struct {
		name            string
		plan            *Plan
		completeRequest bool
		wantStatus      TaskStatus
		wantPhase       string
		wantNext        string
		wantCompleted   int
		wantFailures    int
	}{
		{
			name:          "in progress with next action",
			plan:          &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, pending}},
			wantStatus:    TaskStatusActive,
			wantPhase:     "1/2 完成",
			wantNext:      "验证迁移",
			wantCompleted: 1,
		},
		{
			name:          "all nodes succeeded",
			plan:          &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, {ID: "n2", Goal: "验证迁移", Status: PlanNodeStatusSucceeded}}},
			wantStatus:    TaskStatusCompleted,
			wantPhase:     "2/2 完成",
			wantNext:      "",
			wantCompleted: 2,
		},
		{
			name:          "plan completed status",
			plan:          &Plan{ID: "p1", Status: PlanStatusCompleted, Nodes: []PlanNode{succeeded}},
			wantStatus:    TaskStatusCompleted,
			wantPhase:     "1/1 完成",
			wantCompleted: 1,
		},
		{
			name:            "complete requested by tool",
			plan:            &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{pending}},
			completeRequest: true,
			wantStatus:      TaskStatusCompleted,
			wantPhase:       "0/1 完成",
			wantNext:        "",
		},
		{
			name:          "failed node counted and blocks next",
			plan:          &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, pending, failed}},
			wantStatus:    TaskStatusActive,
			wantPhase:     "1/3 完成",
			wantNext:      "验证迁移",
			wantCompleted: 1,
			wantFailures:  1,
		},
		{
			name:       "nil plan",
			plan:       nil,
			wantStatus: TaskStatusActive,
		},
		{
			name:       "empty nodes",
			plan:       &Plan{ID: "p1", Status: PlanStatusActive},
			wantStatus: TaskStatusActive,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := BuildTaskSnapshot(tc.plan, tc.completeRequest)
			if snapshot.Status != tc.wantStatus {
				t.Fatalf("status: got %q want %q", snapshot.Status, tc.wantStatus)
			}
			if snapshot.CurrentPhase != tc.wantPhase {
				t.Fatalf("phase: got %q want %q", snapshot.CurrentPhase, tc.wantPhase)
			}
			if snapshot.NextAction != tc.wantNext {
				t.Fatalf("next: got %q want %q", snapshot.NextAction, tc.wantNext)
			}
			if len(snapshot.CompletedSteps) != tc.wantCompleted {
				t.Fatalf("completed: got %d want %d", len(snapshot.CompletedSteps), tc.wantCompleted)
			}
			if snapshot.Failures != tc.wantFailures {
				t.Fatalf("failures: got %d want %d", snapshot.Failures, tc.wantFailures)
			}
		})
	}
}

func TestTaskSnapshotToTask(t *testing.T) {
	snapshot := TaskSnapshot{
		Goal: "迁移订单服务", CurrentPhase: "1/2 完成",
		CompletedSteps: []string{"n1"}, NextAction: "验证迁移", Status: TaskStatusActive, Failures: 1,
	}
	task := snapshot.ToTask("p1", "agent-1", "user-1", "11111111-1111-1111-1111-111111111111", "exec-1")
	if task.ID != "p1" || task.AgentID != "agent-1" || task.UserID != "user-1" {
		t.Fatalf("identity mismatch: %+v", task)
	}
	if task.ClaimedBy != "11111111-1111-1111-1111-111111111111" || task.LastExecutionID != "exec-1" {
		t.Fatalf("reference mismatch: %+v", task)
	}
	if task.FailCount != 1 || task.Status != TaskStatusActive {
		t.Fatalf("snapshot fields not copied: %+v", task)
	}
	if task.Generation != 0 {
		t.Fatalf("new task generation should be 0, got %d", task.Generation)
	}
	if !task.LeaseExpiresAt.After(time.Now()) || !task.ExpiresAt.After(time.Now().Add(constants.TaskExpiresAt-time.Hour)) {
		t.Fatalf("lease/expiry not set: lease=%s expiry=%s", task.LeaseExpiresAt, task.ExpiresAt)
	}
}
