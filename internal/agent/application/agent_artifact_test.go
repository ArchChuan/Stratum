package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
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

func TestBuildExecutionArtifactsDeduplicatesAndBoundsAggregate(t *testing.T) {
	citation := domain.Citation{DocumentID: "doc", Title: strings.Repeat("t", 900), Excerpt: "Authorization: Bearer raw-secret"}
	fact := domain.DiagnosticFact{Area: domain.DiagnosticAreaAgent, Statement: strings.Repeat("f", 900), Source: "source"}
	toolArtifacts := make([]domain.SystemAssistantToolArtifact, 0, 200)
	for i := 0; i < 200; i++ {
		toolArtifacts = append(toolArtifacts,
			domain.SystemAssistantToolArtifact{Tool: "stratum_search_official_docs", Outcome: "success", Citations: []domain.Citation{citation}},
			domain.SystemAssistantToolArtifact{Tool: "stratum_diagnose_tenant", Outcome: "success", Evidence: &domain.DiagnosticEvidence{Facts: []domain.DiagnosticFact{fact}}})
	}
	first := buildExecutionArtifacts(toolArtifacts, "v1")
	second := buildExecutionArtifacts(toolArtifacts, "v1")
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), constants.SystemAssistantToolMaxJSONBytes)
	require.NotContains(t, string(raw), "raw-secret")
	require.Equal(t, first, second)
	require.Len(t, first[0].Citations, 1)
	require.Empty(t, first[1].DiagnosticReport.Citations)
}
