package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// stratumSkillToolName 是统一 skill 触发工具的保留名。发现触发对齐后工具面从
// "每 skill 一工具"收敛为单一 stratum_skill 工具（Spec D1），按参数 skill 分发；
// 该名字不与任何 skill 激活名冲突（buildSkillCatalog 侧拒绝保留名）。
const stratumSkillToolName = "stratum_skill"

// IsReservedToolName 报告 name 是否命中平台内置工具保留名。skill 激活名冲突时
// 由 buildSkillCatalog fail-closed 拒绝，避免统一工具分发被内置工具截胡或歧义。
func IsReservedToolName(name string) bool {
	switch name {
	case stratumSkillToolName, "stratum_search_knowledge", "stratum_recall_memory",
		"stratum_continue_reasoning", "stratum_create_plan", "stratum_revise_plan",
		"stratum_continue_plan", "stratum_cancel_plan", "stratum_complete_task":
		return true
	default:
		return false
	}
}

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

// buildSkillTool builds the single unified stratum_skill tool definition whose
// description lists every skill in the agent-bound catalog (Spec D1). The
// description is sized to the two-stage token allowance so the tool itself
// always fits fitToolList and is never dropped whole: stage 1 computes the
// allowance in prepareLLMRequest, stage 2 truncates here. allowance <= 0 means
// budget uninitialized (fitToolsToContextBudget early-returns), so the full
// listing is kept. Returns nil when the catalog is empty (no skill bindings).
func buildSkillTool(catalog map[string]port.SkillActivation, actives []port.SkillActivation, allowance int) *port.ToolDefinition {
	if len(catalog) == 0 {
		return nil
	}
	lines := buildSkillCatalogLines(catalog, actives)
	return &port.ToolDefinition{
		Name:        stratumSkillToolName,
		Description: fitSkillCatalogDescription(lines, allowance),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{"type": "string"},
			},
			"required": []any{"skill"},
		},
		ProviderType: domain.ProviderTypeSkill,
		ProviderID:   stratumSkillToolName,
		CapabilityID: stratumSkillToolName,
		NodeID:       stratumSkillToolName,
		NodeType:     domain.ObservationTypeSkill,
	}
}

// buildSkillCatalogLines 逐行列出绑定集合内每个 skill 的 name+description；
// 已激活 skill 标注 "(已激活)"，让模型从列表即可判断生效状态、从源头减少重复
// 激活（Spec D6 可选增强）。
func buildSkillCatalogLines(catalog map[string]port.SkillActivation, actives []port.SkillActivation) []string {
	activeByName := make(map[string]bool, len(actives))
	for _, a := range actives {
		activeByName[activationName(a)] = true
	}
	ids := make([]string, 0, len(catalog))
	for id := range catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		a := catalog[id]
		name := activationName(a)
		marker := ""
		if activeByName[name] {
			marker = " (已激活)"
		}
		lines = append(lines, fmt.Sprintf("- %s%s: %s", name, marker, a.Description))
	}
	return lines
}

// fitSkillCatalogDescription 把 skill 列表描述压缩进 token allowance（Spec D1
// 两阶段截断阶段二）：先逐条缩短 description（上限按行数均分预算），再从尾部
// 整条略去，保证列表骨架完整。截断以工具实际 JSON 编码估算，确保
// EstimateText(encoded) <= allowance，stratum_skill 因此在 fitToolList 贪心
// 打包时恒被保留（置于工具面首位）。allowance <= 0 视为预算未初始化，返回全文。
func fitSkillCatalogDescription(lines []string, allowance int) string {
	if allowance <= 0 {
		return strings.Join(lines, "\n")
	}
	encoded, _ := json.Marshal(port.ToolDefinition{Name: stratumSkillToolName, InputSchema: skillInputSchema()})
	overhead := len(encoded) // Description="" 时的固定 JSON 开销
	descBudget := allowance*3 - overhead
	if descBudget <= 0 {
		return ""
	}
	// 阶段 A：逐条缩短 description，上限按行数均分（至少保留名称行）。
	perLine := max(descBudget/len(lines), 8)
	short := make([]string, 0, len(lines))
	for _, line := range lines {
		if r := []rune(line); len(r) > perLine {
			line = string(r[:perLine]) + "…"
		}
		short = append(short, line)
	}
	// 阶段 B：从尾部整条略去，直到编码后 fit（JSON 转义按实际 marshal 计算）。
	for i := len(short); i > 0; i-- {
		candidate := strings.Join(short[:i], "\n")
		tool := port.ToolDefinition{Name: stratumSkillToolName, Description: candidate, InputSchema: skillInputSchema()}
		if encoded, err := json.Marshal(tool); err == nil && tokenutil.EstimateText(string(encoded)) <= allowance {
			return candidate
		}
	}
	return ""
}

func skillInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"skill": map[string]any{"type": "string"},
		},
		"required": []any{"skill"},
	}
}

// effectiveTools 组装本步分发到 LLM 的工具集（Spec D5）：plan 工作流工具 + agent
// 绑定全集（MCP + 内置知识/记忆/推理）。激活 skill 不再收窄工具面——工具面恒等于
// agent 绑定全集；stratum_skill 统一工具由 prepareLLMRequest 按预算两阶段截断后
// 置于首位并入，不在此静态生成。governed assistant（系统助手）同样追加 plan 工具：
// plan 工作流是 agent 目标持久化（agent_tasks）的触发源，系统助手是唯一真实用户
// 路径，不追加则跨会话 task 链路对用户不可见；plan 工具仅操作内存/checkpoint 状态，
// 无外部副作用，与角色能力契约无关。
func effectiveTools(available []port.ToolDefinition, governedAssistant bool) []port.ToolDefinition {
	out := make([]port.ToolDefinition, 0, len(available)+len(PlanToolDefinitions()))
	out = append(out, PlanToolDefinitions()...)
	for _, tool := range available {
		// 防御不变量：plan 工具由 effectiveTools 单独追加、不在 AvailableTools，
		// 若上游意外混入则去重，避免重复暴露。
		if isReservedPlanTool(tool.Name) {
			continue
		}
		out = append(out, tool)
	}
	// governedAssistant 不再影响工具面（plan 工具对系统助手同样暴露，见函数
	// 注释）；其唯一剩余语义在 prepareLLMRequest 的裁剪跳过（角色能力契约）。
	_ = governedAssistant
	return out
}

func isReservedPlanTool(name string) bool {
	switch name {
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan", "stratum_complete_task":
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

// activationName 返回 skill 的解析名：ActivationContract.Name 优先，空则回退
// SkillID（buildSkillCatalog 已保证集合内唯一）。
func activationName(a port.SkillActivation) string {
	if a.Name != "" {
		return a.Name
	}
	return a.SkillID
}

// skillParallelConflictNote 是 D3 的显式冲突声明：多 skill 并列生效、冲突由模型
// 自决。靠声明不靠位置——压缩截断按 size 不按顺序，位置/顺序语义会被打乱。
const skillParallelConflictNote = "多个 skill 并列生效，指令冲突时由模型按任务意图自行取舍。"

func messagesWithActiveSkills(messages []port.LLMMessage, actives []port.SkillActivation) []port.LLMMessage {
	var instructions []port.LLMMessage
	for _, active := range actives {
		if active.Instructions == "" {
			continue
		}
		instructions = append(instructions, port.LLMMessage{
			Role:    "system",
			Content: fmt.Sprintf("Active Skill %s (revision %s):\n%s", activationName(active), active.RevisionID, active.Instructions),
		})
	}
	if len(instructions) == 0 {
		return messages
	}
	if len(instructions) > 1 {
		// D3：多个 skill 并列生效时显式声明冲突自决语义；单个 skill 无冲突不赘述。
		instructions = append([]port.LLMMessage{{Role: "system", Content: skillParallelConflictNote}}, instructions...)
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
