// Test-only exports — visible to external test packages (graph_test)
// but not compiled into the production binary.

package graph

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// Test-only aliases for unexported helpers.
func MessagesWithActiveSkillsForTest(messages []port.LLMMessage, actives []port.SkillActivation) []port.LLMMessage {
	return messagesWithActiveSkills(messages, actives)
}

func AllowedKnowledgeWorkspacesForTest(requested, agentAllowed []string) []string {
	return allowedKnowledgeWorkspaces(requested, agentAllowed)
}

func EffectiveToolsForTest(available []port.ToolDefinition) []port.ToolDefinition {
	return effectiveTools(available)
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

// P2 stop-loss 治理的 test-only 导出。recordToolFailure 是 makeToolNode 的通用
// 计数点（status==Error），recordCorrection 是 plan-tool 的 correction 计数点
// （status=Success）；两者是互斥路径，都进同一 stop-loss 门。
func RecordToolFailureForTest(s *ReActState, toolName, errMsg string) {
	s.recordToolFailure(toolName, errMsg)
}

func RecordCorrectionForTest(s *ReActState, toolName string, err error, plan *domain.Plan) string {
	return s.recordCorrection(toolName, err, plan)
}

func PrepareLLMRequestForTest(ctx context.Context, s *ReActState) ([]port.ToolDefinition, []port.LLMMessage, int) {
	return prepareLLMRequest(ctx, s)
}

func PlanSlotChildStateForTest(s ReActState) ReActState {
	return planSlotChildState(s)
}

func MakeToolNodeForTest(capGW port.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
	return makeToolNode(capGW, logger)
}

func MakeLLMNodeForTest(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) NodeFunc[ReActState] {
	return makeLLMNode(capGW, ledger, logger)
}
