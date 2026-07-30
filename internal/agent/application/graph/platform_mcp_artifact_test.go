package graph

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/stretchr/testify/require"
)

func TestPlatformMCPArtifactBuildsTypedEvidenceFromGuardedContent(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		structured map[string]any
		assert     func(*testing.T, *domain.SystemAssistantToolArtifact)
	}{
		{name: "docs", tool: platformmcp.ToolSearchOfficialDocs, structured: map[string]any{
			"citations": []any{map[string]any{"title": "Agent", "url": "/docs/agent"}},
		}, assert: func(t *testing.T, artifact *domain.SystemAssistantToolArtifact) {
			require.Len(t, artifact.Citations, 1)
		}},
		{name: "diagnostics", tool: platformmcp.ToolDiagnoseTenant, structured: map[string]any{
			"evidence": map[string]any{"gaps": []any{map[string]any{"area": "mcp", "code": "evidence_unavailable"}}},
		}, assert: func(t *testing.T, artifact *domain.SystemAssistantToolArtifact) {
			require.NotNil(t, artifact.Evidence)
			require.Len(t, artifact.Evidence.Gaps, 1)
		}},
		{name: "proposal", tool: platformmcp.ToolProposeResourceChange, structured: map[string]any{
			"id": "proposal-1", "resourceKind": "agent", "operation": "create", "status": "ready_for_review",
		}, assert: func(t *testing.T, artifact *domain.SystemAssistantToolArtifact) {
			require.Equal(t, "proposal-1", artifact.Proposal.ID)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			artifact := platformMCPArtifact(tc.tool, tc.structured, 12)
			require.NotNil(t, artifact)
			require.Equal(t, tc.tool, artifact.Tool)
			tc.assert(t, artifact)
		})
	}
}

func TestPlatformMCPArtifactRejectsUnknownOrMalformedContent(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		structured map[string]any
	}{
		{name: "unknown tool", tool: "tenant_tool", structured: map[string]any{}},
		{name: "proposal without ID", tool: platformmcp.ToolProposeResourceChange, structured: map[string]any{}},
		{name: "missing content", tool: platformmcp.ToolDiagnoseTenant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Nil(t, platformMCPArtifact(tc.tool, tc.structured, 1))
		})
	}
}
