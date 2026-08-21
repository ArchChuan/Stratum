package domain

import (
	"testing"
)

func TestTrajectorySkeleton_Validate(t *testing.T) {
	ok := TrajectorySkeleton{
		ExecutionID: "exec-1",
		Steps: []TrajectoryStep{
			{ToolName: "search", Status: TrajectoryStepStatusSuccess},
		},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid skeleton rejected: %v", err)
	}

	cases := []struct {
		name string
		sk   TrajectorySkeleton
	}{
		{name: "empty execution id", sk: TrajectorySkeleton{Steps: ok.Steps}},
		{name: "no steps", sk: TrajectorySkeleton{ExecutionID: "e1"}},
		{name: "empty tool name", sk: TrajectorySkeleton{ExecutionID: "e1", Steps: []TrajectoryStep{{Status: TrajectoryStepStatusSuccess}}}},
		{name: "invalid status", sk: TrajectorySkeleton{ExecutionID: "e1", Steps: []TrajectoryStep{{ToolName: "x", Status: "bogus"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.sk.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestTrajectorySkeleton_ShouldReflect(t *testing.T) {
	min := 3
	oneStep := []TrajectoryStep{{ToolName: "search", Status: TrajectoryStepStatusSuccess}}
	threeSteps := []TrajectoryStep{
		{ToolName: "search", Status: TrajectoryStepStatusSuccess},
		{ToolName: "read", Status: TrajectoryStepStatusSuccess},
		{ToolName: "write", Status: TrajectoryStepStatusSuccess},
	}
	failedStep := []TrajectoryStep{{ToolName: "search", Status: TrajectoryStepStatusError, ErrorFingerprint: "boom"}}

	cases := []struct {
		name     string
		steps    []TrajectoryStep
		explicit bool
		want     bool
	}{
		{name: "no steps never reflects", steps: nil, want: false},
		{name: "single read-only query filtered", steps: oneStep, want: false},
		{name: "tool count threshold triggers", steps: threeSteps, want: true},
		{name: "failure triggers regardless of count", steps: failedStep, want: true},
		{name: "explicit memory instruction triggers", steps: oneStep, explicit: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sk := TrajectorySkeleton{ExecutionID: "e1", Steps: tc.steps}
			if got := sk.ShouldReflect(min, tc.explicit); got != tc.want {
				t.Fatalf("ShouldReflect=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestTrajectorySkeleton_AggregateToolStats(t *testing.T) {
	sk := TrajectorySkeleton{
		ExecutionID: "e1",
		Steps: []TrajectoryStep{
			{ToolName: "search", Status: TrajectoryStepStatusSuccess},
			{ToolName: "search", Status: TrajectoryStepStatusError},
			{ToolName: "read", Status: TrajectoryStepStatusSuccess},
		},
	}
	stats := sk.AggregateToolStats()
	if stats["search"].Count != 2 || stats["search"].ErrorCount != 1 {
		t.Fatalf("unexpected search stats: %#v", stats["search"])
	}
	if stats["read"].Count != 1 || stats["read"].ErrorCount != 0 {
		t.Fatalf("unexpected read stats: %#v", stats["read"])
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("你好世界", 2); got != "你好" {
		t.Fatalf("TruncateRunes(2)=%q", got)
	}
	if got := TruncateRunes("short", 100); got != "short" {
		t.Fatalf("TruncateRunes(100)=%q", got)
	}
}
