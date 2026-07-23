package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

func TestBuildExecutionArtifactsRetainsSuccessfulRunToolFailure(t *testing.T) {
	artifacts := buildExecutionArtifacts([]domain.SystemAssistantToolArtifact{{
		Tool: "stratum_diagnose_tenant", Outcome: "error", ErrorCode: "timeout", LatencyMs: 23,
	}}, "profile-v1")
	require.Len(t, artifacts, 1)
	require.Equal(t, "profile-v1", artifacts[0].ProfileVersion)
	require.Equal(t, []domain.DiagnosticStep{{Tool: "stratum_diagnose_tenant", Outcome: "error", ErrorCode: "timeout", LatencyMs: 23}}, artifacts[0].DiagnosticReport.Steps)
	require.NotEmpty(t, artifacts[0].DiagnosticReport.EvidenceGaps)
}
