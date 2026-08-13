// Test-only exports — visible to external test packages
// (application_test) but not compiled into the production binary.

package application

import (
	"context"
	"encoding/json"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

func NormalizeMCPToolForTest(tool port.ToolDefinition, serverID string) port.ToolDefinition {
	return normalizeMCPTool(tool, serverID)
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

func ResolveExecutionWindowForTest(
	s *AgentService, ctx context.Context, tenantID, model string, explicit int,
) (int, agentgraph.WindowSource) {
	return s.resolveExecutionWindow(ctx, tenantID, model, explicit)
}

func CatalogFromActivationsForTest(activations []port.SkillActivation) map[string]port.SkillActivation {
	return catalogFromActivations(activations)
}

func RestorePlanCheckpointStateForTest(raw json.RawMessage, catalog map[string]port.SkillActivation) (*domain.Plan, []port.SkillActivation) {
	return restorePlanCheckpointState(raw, catalog)
}

func IsFinalRequestForTest(s agentgraph.ReActState) bool { return isFinalRequest(s) }

func ValidateSkillCatalogNamesForTest(catalog map[string]port.SkillActivation) error {
	return validateSkillCatalogNames(catalog)
}
