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

func AllowedKnowledgeWorkspacesForTest(requested, agentAllowed []string, actives []port.SkillActivation) []string {
	return allowedKnowledgeWorkspaces(requested, agentAllowed, actives)
}

func EffectiveToolsForTest(
	available []port.ToolDefinition,
	catalog map[string]port.SkillActivation,
	actives []port.SkillActivation,
	agentKnowledgeWorkspaceIDs []string,
	agentMemoryScope string,
	governedAssistant bool,
) []port.ToolDefinition {
	return effectiveTools(available, catalog, actives, agentKnowledgeWorkspaceIDs, agentMemoryScope, governedAssistant)
}

func UpsertActivationForTest(actives []port.SkillActivation, activation port.SkillActivation) []port.SkillActivation {
	return upsertActivation(actives, activation)
}

func RouteLLMForTest(ctx context.Context, s ReActState, messages []port.LLMMessage, tools []port.ToolDefinition, capGW port.CapabilityGateway) (port.CapabilityResponse, error) {
	return routeLLM(ctx, s, messages, tools, capGW)
}
