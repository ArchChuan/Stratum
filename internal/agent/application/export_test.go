// Test-only exports — visible to external test packages
// (application_test) but not compiled into the production binary.

package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

// BuildExtraToolsForTest exposes buildExtraTools to external test packages.
func (s *AgentService) BuildExtraToolsForTest(ctx context.Context, tenantID string, mcpServerIDs, allowedSkills []string) ([]port.ToolDefinition, map[string]port.SkillActivation) {
	return s.buildExtraTools(ctx, tenantID, "test-subject", mcpServerIDs, allowedSkills)
}

// Test-only aliases for pure functions / unexported helpers.
func ParseAgentTypeWireForTest(t string) domain.AgentType { return parseAgentTypeWire(t) }

func ExecutionSubjectForTest(req ExecRequest, meta ExecMeta) string {
	return executionSubject(req, meta)
}

func ApplyAgentAssignmentForTest(meta *ExecMeta, agentID string, assignment port.AgentRevisionAssignment) {
	applyAgentAssignment(meta, agentID, assignment)
}

func HasFailedAssistantArtifactForTest(result *AgentResult) bool {
	return hasFailedAssistantArtifact(result)
}

func BoundedAssistantRoleClassForTest(role string) string { return boundedAssistantRoleClass(role) }

func WithoutPlatformMCPToolsForTest(toolIDs []string) []string {
	return withoutPlatformMCPTools(toolIDs)
}

func NormalizeMCPToolForTest(tool port.ToolDefinition, serverID string) port.ToolDefinition {
	return normalizeMCPTool(tool, serverID)
}

func PlatformMCPRiskForTest(risk platformmcp.RiskLevel) port.ToolRiskLevel {
	return platformMCPRisk(risk)
}

func StricterToolRiskForTest(left, right port.ToolRiskLevel) port.ToolRiskLevel {
	return stricterToolRisk(left, right)
}

func ToolRiskRankForTest(risk port.ToolRiskLevel) int { return toolRiskRank(risk) }

func TruncateRunesForTest(s string, maxRunes int) string { return truncateRunes(s, maxRunes) }

func ExecutionIDOrNewForTest(id string) string { return executionIDOrNew(id) }

func ApplySkillAssignmentsForTest(catalog map[string]port.SkillActivation, assignments map[string]port.SkillRevisionAssignment) {
	applySkillAssignments(catalog, assignments)
}

func DeriveMaxContextTokensForTest(s *AgentService, ctx context.Context, tenantID, model string) int {
	return s.deriveMaxContextTokens(ctx, tenantID, model)
}
