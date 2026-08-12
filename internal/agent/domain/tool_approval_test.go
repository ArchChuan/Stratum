package domain

import "testing"

func TestValidateToolApprovalTransitionTerminalStates(t *testing.T) {
	cases := []struct {
		name string
		from ToolApprovalStatus
		to   ToolApprovalStatus
		ok   bool
	}{
		{"pending to cancelled", ToolApprovalPending, ToolApprovalCancelled, true},
		{"approved to voided", ToolApprovalApproved, ToolApprovalVoided, true},
		{"approved to invalidated", ToolApprovalApproved, ToolApprovalInvalidated, true},
		{"executing to invalidated", ToolApprovalExecuting, ToolApprovalInvalidated, true},
		{"terminal cancelled no further", ToolApprovalCancelled, ToolApprovalApproved, false},
		{"terminal voided no further", ToolApprovalVoided, ToolApprovalExecuting, false},
		{"terminal invalidated no further", ToolApprovalInvalidated, ToolApprovalExecuted, false},
		{"pending to voided not allowed", ToolApprovalPending, ToolApprovalVoided, false},
		{"approved to cancelled not allowed", ToolApprovalApproved, ToolApprovalCancelled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateToolApprovalTransition(tc.from, tc.to)
			if tc.ok && err != nil {
				t.Fatalf("expected allowed, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected error for %s -> %s", tc.from, tc.to)
			}
		})
	}
}

func TestValidateSubjectKind(t *testing.T) {
	for _, kind := range []string{SubjectKindMCPTool, SubjectKindEvaluationAction, SubjectKindMCPPolicy, SubjectKindMCPServer, ""} {
		if err := ValidateSubjectKind(kind); err != nil {
			t.Fatalf("expected %q valid, got %v", kind, err)
		}
	}
	for _, kind := range []string{"unknown_kind", "mcp-tool", "eval"} {
		if err := ValidateSubjectKind(kind); err == nil {
			t.Fatalf("expected %q invalid", kind)
		}
	}
}
