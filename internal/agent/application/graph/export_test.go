// Test-only exports — visible to external test packages (graph_test)
// but not compiled into the production binary.

package graph

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// Test-only aliases for unexported helpers.
func MessagesWithActiveSkillsForTest(messages []port.LLMMessage, actives []port.SkillActivation) []port.LLMMessage {
	return messagesWithActiveSkills(messages, actives)
}

func AllowedKnowledgeWorkspacesForTest(requested, agentAllowed []string) []string {
	return allowedKnowledgeWorkspaces(requested, agentAllowed)
}

func EffectiveToolsForTest(
	available []port.ToolDefinition,
	governedAssistant bool,
) []port.ToolDefinition {
	return effectiveTools(available, governedAssistant)
}

func BuildSkillToolForTest(catalog map[string]port.SkillActivation, actives []port.SkillActivation, allowance int) *port.ToolDefinition {
	return buildSkillTool(catalog, actives, allowance)
}

func BuildSkillCatalogLinesForTest(catalog map[string]port.SkillActivation, actives []port.SkillActivation) []string {
	return buildSkillCatalogLines(catalog, actives)
}

func UpsertActivationForTest(actives []port.SkillActivation, activation port.SkillActivation) []port.SkillActivation {
	return upsertActivation(actives, activation)
}

func RouteLLMForTest(ctx context.Context, s ReActState, messages []port.LLMMessage, tools []port.ToolDefinition, capGW port.CapabilityGateway) (port.CapabilityResponse, error) {
	return routeLLM(ctx, s, messages, tools, capGW)
}
