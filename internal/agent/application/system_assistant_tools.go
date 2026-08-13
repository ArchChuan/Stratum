package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
)

const (
	ToolSearchOfficialDocs    = domain.SystemAssistantToolSearchOfficialDocs
	ToolDiagnoseTenant        = domain.SystemAssistantToolDiagnoseTenant
	ToolProposeResourceChange = domain.SystemAssistantToolProposeResourceChange
	ToolApplyResourceChange   = domain.SystemAssistantToolApplyResourceChange
	ToolListModels            = domain.SystemAssistantToolListModels
	ToolUpdateSystemModel     = domain.SystemAssistantToolUpdateSystemModel
)

func SystemAssistantToolDefinitions() []port.ToolDefinition {
	return []port.ToolDefinition{
		{
			Name: ToolSearchOfficialDocs, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolSearchOfficialDocs, CapabilityID: ToolSearchOfficialDocs,
			Description: "检索当前版本的 Stratum 官方文档。仅在需要回答平台使用方式时调用。",
			InputSchema: searchDocsSchema(),
		},
		{
			Name: ToolDiagnoseTenant, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolDiagnoseTenant, CapabilityID: ToolDiagnoseTenant,
			Description: "按当前登录成员权限只读诊断当前租户的应用状态。",
			InputSchema: diagnoseTenantSchema(),
		},
		{
			Name: ToolListModels, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolListModels, CapabilityID: ToolListModels,
			Description: "列出当前租户全量可配置模型（含停用/embedding，标注 enabled 与能力）。",
			InputSchema: jschema.Must(jschema.ClosedObject()).Map(),
		},
		{
			Name: ToolUpdateSystemModel, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolUpdateSystemModel, CapabilityID: ToolUpdateSystemModel,
			Description: "更新平台助手（系统助手）使用的模型。需要管理员权限，member 调用会被拒绝。",
			InputSchema: jschema.Must(jschema.ClosedObject(
				jschema.RequiredProp("model", jschema.StringRange(1, 0, "")),
			)).Map(),
		},
		{
			Name: ToolProposeResourceChange, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolProposeResourceChange, CapabilityID: ToolProposeResourceChange,
			Description: "创建受治理的资源变更提案：admin/owner 调用后自动确认并应用，member 生成待审提案等待审批。",
			InputSchema: proposalToolSchema(),
		},
		{
			Name: ToolApplyResourceChange, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolApplyResourceChange, CapabilityID: ToolApplyResourceChange,
			Description: "直接修改租户资源(更新或创建)，无需审批，修改会立即生效并记录审计。调用前必须从对话确认用户意图；仅用于用户明确要求立即生效的场景，否则应使用提案工具。禁止删除资源、修改凭据或发布操作。",
			InputSchema: proposalToolSchema(),
		},
	}
}

func proposalToolSchema() map[string]any {
	payloads := map[domain.ResourceKind]*jschema.Schema{
		domain.ResourceAgent: jschema.Must(jschema.ClosedObject(
			jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
			jschema.RequiredProp("description", jschema.String("")),
			jschema.RequiredProp("model", jschema.String("")),
			jschema.RequiredProp("maxIterations", jschema.Integer(jschema.Ptr(1), jschema.Ptr(20), "")),
			jschema.RequiredProp("maxContextTokens", jschema.Integer(jschema.Ptr(1), nil, "")),
			jschema.OptionalProp("skillIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
			jschema.OptionalProp("mcpToolIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
			jschema.OptionalProp("workspaceIds", jschema.Array(jschema.String(""), 0, 0, true, "")),
		)),
		domain.ResourceSkillDraft: jschema.Must(jschema.ClosedObject(
			jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
			jschema.RequiredProp("description", jschema.String("")),
			jschema.RequiredProp("instructions", jschema.StringRange(1, 0, "")),
		)),
		domain.ResourceMCPConfig: jschema.Must(jschema.ClosedObject(
			jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
			jschema.RequiredProp("version", jschema.String("")),
			jschema.RequiredProp("transport", jschema.Must(jschema.Enum("", "streamable-http"))),
			jschema.RequiredProp("timeoutSec", jschema.Integer(jschema.Ptr(minProposalMCPTimeoutSec), jschema.Ptr(maxProposalMCPTimeoutSec), "")),
			jschema.OptionalProp("command", jschema.String("")),
			jschema.OptionalProp("args", jschema.Array(jschema.String(""), 0, 0, false, "")),
			jschema.OptionalProp("url", jschema.String("")),
			jschema.OptionalProp("capabilities", jschema.Array(jschema.String(""), 0, 0, true, "")),
			jschema.OptionalProp("retry", proposalRetrySchema()),
		)),
		domain.ResourceKnowledgeWorkspace: jschema.Must(jschema.ClosedObject(
			jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
			jschema.RequiredProp("description", jschema.StringRange(1, 0, "")),
			jschema.RequiredProp("embeddingModel", jschema.String("")),
		)),
	}
	kinds := []domain.ResourceKind{
		domain.ResourceAgent, domain.ResourceSkillDraft, domain.ResourceMCPConfig, domain.ResourceKnowledgeWorkspace,
	}
	branches := make([]*jschema.Schema, 0, len(kinds)*2)
	for _, kind := range kinds {
		for _, operation := range []domain.ProposalOperation{domain.OperationCreate, domain.OperationUpdate} {
			props := []jschema.Prop{
				jschema.RequiredProp("resourceKind", jschema.Const(string(kind))),
				jschema.RequiredProp("operation", jschema.Const(string(operation))),
				jschema.RequiredProp("payload", payloads[kind]),
			}
			if operation == domain.OperationUpdate {
				props = append(props, jschema.RequiredProp("resourceId", jschema.StringRange(1, 0, "")))
			}
			branches = append(branches, jschema.Must(jschema.ClosedObject(props...)))
		}
	}
	return jschema.Must(jschema.OneOf(branches...)).Map()
}

func proposalRetrySchema() *jschema.Schema {
	return jschema.Must(jschema.ClosedObject(
		jschema.RequiredProp("enabled", jschema.Boolean("")),
		jschema.RequiredProp("maxRetries", jschema.Integer(jschema.Ptr(minProposalMCPRetryCount), jschema.Ptr(maxProposalMCPRetryCount), "")),
		jschema.RequiredProp("initialDelayMs", jschema.Integer(jschema.Ptr(int(minProposalMCPRetryInitialDelayMs)), jschema.Ptr(int(maxProposalMCPRetryInitialDelayMs)), "")),
		jschema.RequiredProp("maxDelayMs", jschema.Integer(jschema.Ptr(int(minProposalMCPRetryMaxDelayMs)), jschema.Ptr(int(maxProposalMCPRetryMaxDelayMs)), "")),
		jschema.RequiredProp("backoffFactor", jschema.Number(jschema.Ptr(minProposalMCPRetryBackoffFactor), jschema.Ptr(maxProposalMCPRetryBackoffFactor), "")),
	))
}

func searchDocsSchema() map[string]any {
	return jschema.Must(jschema.ClosedObject(
		jschema.RequiredProp("query", jschema.StringRange(1, constants.SystemAssistantQueryMaxRunes, "")),
	)).Map()
}

func diagnoseTenantSchema() map[string]any {
	areas := jschema.Array(
		jschema.Must(jschema.Enum("", "agent", "skill", "mcp", "knowledge", "model")),
		1, constants.SystemAssistantAreasMaxCount, true, "",
	)
	return jschema.Must(jschema.ClosedObject(
		jschema.RequiredProp("areas", areas),
	)).Map()
}

func ParseResourceChangeToolArguments(args map[string]any) (domain.ResourceKind, domain.ProposalOperation, string, []byte, error) {
	allowed := map[string]bool{"resourceKind": true, "operation": true, "resourceId": true, "payload": true}
	for key := range args {
		if !allowed[key] {
			return "", "", "", nil, fmt.Errorf("%w: unknown field %s", domain.ErrInvalidSystemAssistantToolArguments, key)
		}
	}
	kind, kindOK := args["resourceKind"].(string)
	operation, operationOK := args["operation"].(string)
	payload, payloadOK := normalizeToolPayload(args["payload"])
	resourceID, _ := args["resourceId"].(string)
	if !kindOK || !operationOK || !payloadOK {
		return "", "", "", nil, domain.ErrInvalidSystemAssistantToolArguments
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", nil, domain.ErrInvalidSystemAssistantToolArguments
	}
	resourceKind := domain.ResourceKind(kind)
	proposalOperation := domain.ProposalOperation(operation)
	if !resourceKind.Valid() || !proposalOperation.Valid() {
		return "", "", "", nil, domain.ErrInvalidSystemAssistantToolArguments
	}
	return resourceKind, proposalOperation, resourceID, raw, nil
}

// normalizeToolPayload 容忍模型在严格嵌套对象 schema 下把 payload 序列化成
// JSON 字符串的常见偏差（生产实测 glm-5 会传 payload="{\"name\": ...}"），
// 归一化为 map 后统一走后续字段校验；非字符串或解析失败保持原样失败。
func normalizeToolPayload(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	raw, ok := value.(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}
