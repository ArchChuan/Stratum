package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolApprovalStateTransitions(t *testing.T) {
	tests := []struct {
		from, to ToolApprovalStatus
		allowed  bool
	}{
		{ToolApprovalPending, ToolApprovalApproved, true},
		{ToolApprovalPending, ToolApprovalRejected, true},
		{ToolApprovalPending, ToolApprovalExpired, true},
		{ToolApprovalApproved, ToolApprovalExecuting, true},
		{ToolApprovalExecuting, ToolApprovalExecuted, true},
		{ToolApprovalExecuting, ToolApprovalApproved, true},
		{ToolApprovalExecuting, ToolApprovalOutcomeUnknown, true},
		{ToolApprovalExecuted, ToolApprovalExecuting, false},
		{ToolApprovalOutcomeUnknown, ToolApprovalExecuting, false},
		{ToolApprovalRejected, ToolApprovalApproved, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			err := ValidateToolApprovalTransition(tt.from, tt.to)
			if tt.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
