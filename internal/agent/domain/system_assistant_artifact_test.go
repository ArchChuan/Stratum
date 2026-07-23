package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDiagnosticReportPreservesSafeFailureStep(t *testing.T) {
	report := BuildDiagnosticReport([]SystemAssistantToolArtifact{{
		Tool: "stratum_diagnose_tenant", Outcome: "error", ErrorCode: "timeout", LatencyMs: 37,
	}})
	require.Equal(t, []DiagnosticStep{{Tool: "stratum_diagnose_tenant", Outcome: "error", ErrorCode: "timeout", LatencyMs: 37}}, report.Steps)
	require.Equal(t, []EvidenceGap{{Source: "stratum_diagnose_tenant", Code: "timeout"}}, report.EvidenceGaps)
	require.Empty(t, report.Inferences)
}

func TestBuildDiagnosticReportPreservesDocsFailureStepWithoutInventingClaims(t *testing.T) {
	report := BuildDiagnosticReport([]SystemAssistantToolArtifact{{
		Tool: "stratum_search_official_docs", Outcome: "error", ErrorCode: "not_found", LatencyMs: 9,
	}})
	require.Equal(t, []DiagnosticStep{{Tool: "stratum_search_official_docs", Outcome: "error", ErrorCode: "not_found", LatencyMs: 9}}, report.Steps)
	require.Empty(t, report.Facts)
	require.Equal(t, []EvidenceGap{{Source: "stratum_search_official_docs", Code: "not_found"}}, report.EvidenceGaps)
}
