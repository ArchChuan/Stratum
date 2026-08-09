package graph

import (
	"context"
	"fmt"
	"sort"

	"go.opentelemetry.io/otel/attribute"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func extractStringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func clampTopK(args map[string]any) int {
	topK := 5
	if v, ok := args["top_k"].(float64); ok {
		topK = int(v)
		if topK > constants.MaxRAGTopK {
			topK = constants.MaxRAGTopK
		}
	}
	return topK
}

func tracePayloadAttributes(
	ctx context.Context,
	store port.TracePayloadStore,
	tenantID, traceID, kind string,
	value any,
) []attribute.KeyValue {
	if !observability.TraceContentCaptureEnabled() || store == nil {
		return nil
	}
	ref, err := store.Put(ctx, port.TracePayload{
		TenantID: tenantID, TraceID: traceID, Kind: kind, Value: value,
	})
	if err != nil {
		return []attribute.KeyValue{
			attribute.String("opik.metadata.stratum.payload_storage_status", "error"),
		}
	}
	return []attribute.KeyValue{
		attribute.String("opik.metadata.stratum.payload_storage_status", "stored"),
		attribute.String("opik.metadata.stratum.payload_ref", ref.Reference),
		attribute.String("opik.metadata.stratum.payload_sha256", ref.SHA256),
		attribute.Int64("opik.metadata.stratum.payload_size_bytes", ref.SizeBytes),
	}
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

// buildSkillToolDefinitions returns one tool definition per skill in the
// catalog, sorted by SkillID for deterministic ordering. Each tool uses the
// ActivationContract name, description, and input schema so the LLM selects
// by semantics rather than by opaque skill ID.
func buildSkillToolDefinitions(catalog map[string]port.SkillActivation) []port.ToolDefinition {
	if len(catalog) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(catalog))
	for id := range catalog {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := make([]port.ToolDefinition, 0, len(sorted))
	for _, skillID := range sorted {
		activation := catalog[skillID]
		schema := activation.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		toolName := activation.Name
		if toolName == "" {
			toolName = activation.SkillID
		}
		out = append(out, port.ToolDefinition{
			Name:         toolName,
			Description:  activation.Description,
			InputSchema:  schema,
			ProviderType: domain.ProviderTypeSkill,
			ProviderID:   activation.SkillID,
			CapabilityID: activation.SkillID,
			NodeID:       toolName,
			NodeType:     domain.ObservationTypeSkill,
		})
	}
	return out
}

func effectiveTools(
	available []port.ToolDefinition,
	catalog map[string]port.SkillActivation,
	actives []port.SkillActivation,
	agentKnowledgeWorkspaceIDs []string,
	agentMemoryScope string,
	governedAssistant bool,
) []port.ToolDefinition {
	if governedAssistant {
		return append([]port.ToolDefinition(nil), available...)
	}
	allowedMCP := map[string]struct{}{}
	for _, active := range actives {
		for _, id := range active.MCPToolIDs {
			allowedMCP[id] = struct{}{}
		}
	}
	out := make([]port.ToolDefinition, 0, len(available)+5)
	out = append(out, PlanToolDefinitions()...)
	out = append(out, buildSkillToolDefinitions(catalog)...)
	for _, tool := range available {
		if toolAllowedByActives(tool, actives, allowedMCP, agentKnowledgeWorkspaceIDs, agentMemoryScope) {
			out = append(out, tool)
		}
	}
	return out
}

// toolAllowedByActives 判断工具是否被允许进入 LLM 工具集：保留 plan 工具恒过滤；
// 无激活技能时其余全部放行；记忆/知识/外部 MCP 工具按激活技能的授权范围过滤。
func toolAllowedByActives(tool port.ToolDefinition, actives []port.SkillActivation, allowedMCP map[string]struct{}, knowledgeWorkspaceIDs []string, memoryScope string) bool {
	if isReservedPlanTool(tool.Name) {
		return false
	}
	if len(actives) == 0 {
		return true
	}
	if tool.Name == "stratum_recall_memory" {
		return anyActiveAllowsMemoryScope(actives, memoryScope)
	}
	if tool.Name == "stratum_search_knowledge" {
		return len(allowedKnowledgeWorkspaces(nil, knowledgeWorkspaceIDs, actives)) > 0
	}
	if tool.ProviderType == domain.ProviderTypeMCP {
		_, ok := allowedMCP[tool.Name]
		return ok
	}
	return true
}

func isReservedPlanTool(name string) bool {
	switch name {
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
		return true
	default:
		return false
	}
}

func withoutPlanTools(tools []port.ToolDefinition) []port.ToolDefinition {
	filtered := make([]port.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		if !isReservedPlanTool(tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// upsertActivation 按 SkillID 原位替换（保留激活顺序）或末尾追加。
func upsertActivation(actives []port.SkillActivation, activation port.SkillActivation) []port.SkillActivation {
	for i, active := range actives {
		if active.SkillID == activation.SkillID {
			actives[i] = activation
			return actives
		}
	}
	return append(actives, activation)
}

// anyActiveAllowsMemoryScope 报告任一 active skill 的 MemoryScopes 包含 scope。
func anyActiveAllowsMemoryScope(actives []port.SkillActivation, scope string) bool {
	for _, active := range actives {
		if containsString(active.MemoryScopes, scope) {
			return true
		}
	}
	return false
}

func messagesWithActiveSkills(messages []port.LLMMessage, actives []port.SkillActivation) []port.LLMMessage {
	var instructions []port.LLMMessage
	for _, active := range actives {
		if active.Instructions == "" {
			continue
		}
		instructions = append(instructions, port.LLMMessage{
			Role:    "system",
			Content: fmt.Sprintf("Active Skill %s (revision %s):\n%s", active.Name, active.RevisionID, active.Instructions),
		})
	}
	if len(instructions) == 0 {
		return messages
	}
	// 多条指令作为连续块整体插入首个 system 消息之后；逐个插入会反转顺序。
	out := make([]port.LLMMessage, 0, len(messages)+len(instructions))
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
		out = append(out, instructions...)
		return append(out, messages[1:]...)
	}
	out = append(out, instructions...)
	return append(out, messages...)
}

func summarizeToolObservation(name, content, status, errMsg string) string {
	if status == domain.ToolTraceStatusError {
		return truncateRunes(fmt.Sprintf("%s failed: %s", name, errMsg), 800)
	}
	return truncateRunes(fmt.Sprintf("%s returned: %s", name, content), 800)
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "...[truncated]"
}
