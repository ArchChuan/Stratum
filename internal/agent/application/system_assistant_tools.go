package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

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
	ToolListAgents            = domain.SystemAssistantToolListAgents
	ToolListMCPServers        = domain.SystemAssistantToolListMCPServers
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
			Name: ToolListAgents, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolListAgents, CapabilityID: ToolListAgents,
			Description: "列出当前租户全部 agent 的安全投影（名称、描述、模型等，不含敏感字段）。",
			InputSchema: jschema.Must(jschema.ClosedObject()).Map(),
		},
		{
			Name: ToolListMCPServers, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolListMCPServers, CapabilityID: ToolListMCPServers,
			Description: "列出当前租户已连接的 MCP server 摘要（名称、状态、传输方式与工具名列表）。",
			InputSchema: jschema.Must(jschema.ClosedObject()).Map(),
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

// proposalPayloadSchemas 是提案/直改工具 payload 的字段级校验单一事实源：
// validateProposalPayloadSchema 运行时校验经此 map 编译校验。模型可见
// InputSchema（proposalToolSchema）只提供顶层松散投影，不再展开这些字段级
// 约束——8 分支 oneOf 全量 schema 会在每轮请求重复透传给 provider，是 prompt
// 膨胀与 prefill 延迟的主要来源（实测系统助手单轮 prompt_tokens≈5.2k 中约 70%
// 来自这两个工具）。字段级校验统一在工具执行边界 fail-closed 执行。
// 每个 resource kind 一个 payload schema（create/update 共用）。
var proposalPayloadSchemas = map[domain.ResourceKind]*jschema.Schema{
	domain.ResourceAgent: jschema.Must(jschema.ClosedObject(
		jschema.RequiredProp("name", jschema.StringRange(1, 0, "")),
		jschema.RequiredProp("description", jschema.String("")),
		jschema.RequiredProp("model", jschema.String("")),
		jschema.RequiredProp("maxIterations", jschema.Integer(jschema.Ptr(constants.MinAgentMaxIterations), jschema.Ptr(constants.MaxAgentMaxIterations), "")),
		jschema.RequiredProp("maxContextTokens", jschema.Integer(jschema.Ptr(1), nil, "")),
		jschema.OptionalProp("systemPrompt", jschema.String("")),
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

// proposalToolSchema 生成提案/直改工具的模型可见 InputSchema（松散投影）。
// 只提示顶层结构：resourceKind/operation 枚举 + payload 为任意对象。payload
// 字段级约束不再展开给模型（每轮透传的 prefill 成本），由执行边界
// ParseResourceChangeToolArguments → validateProposalPayloadSchema 对同一
// proposalPayloadSchemas 运行时校验，失败回传 InvalidToolArgumentsError 中文
// detail 供模型自纠，校验强度与安全路径不变。
func proposalToolSchema() map[string]any {
	return jschema.Must(jschema.ClosedObject(
		jschema.RequiredProp("resourceKind", jschema.Must(jschema.Enum("",
			string(domain.ResourceAgent), string(domain.ResourceSkillDraft),
			string(domain.ResourceMCPConfig), string(domain.ResourceKnowledgeWorkspace)))),
		jschema.RequiredProp("operation", jschema.Must(jschema.Enum("",
			string(domain.OperationCreate), string(domain.OperationUpdate)))),
		jschema.RequiredProp("payload", jschema.Must(jschema.Object())),
		jschema.OptionalProp("resourceId", jschema.StringRange(1, 0, "")),
	)).Map()
}

// validateProposalPayloadSchema 对 normalized payload 做字段级校验，返回携带
// 中文 detail 的 *InvalidToolArgumentsError（模型可读、可自纠）。先查表再
// 编译，未知 kind 直接短路，防 nil deref。
func validateProposalPayloadSchema(resourceKind domain.ResourceKind, payload map[string]any) error {
	schema, ok := proposalPayloadSchemas[resourceKind]
	if !ok {
		return &domain.InvalidToolArgumentsError{Detail: fmt.Sprintf("不支持的资源类型 %s", resourceKind)}
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const schemaURL = "urn:stratum:assistant-proposal-payload"
	if err := compiler.AddResource(schemaURL, schema.Map()); err != nil {
		return &domain.InvalidToolArgumentsError{Detail: "payload 校验规则不可用"}
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return &domain.InvalidToolArgumentsError{Detail: "payload 校验规则不可用"}
	}
	if err := compiled.Validate(payload); err != nil {
		var ve *jsonschema.ValidationError
		if errors.As(err, &ve) {
			return &domain.InvalidToolArgumentsError{Detail: formatProposalValidation(ve)}
		}
		return &domain.InvalidToolArgumentsError{Detail: "payload 不符合字段约束"}
	}
	return nil
}

// formatProposalValidation 把 jsonschema 校验错误格式化为模型可读的中文
// detail。required/additionalProperties 错误时根 InstanceLocation 为空，
// 从 kind 结构手动拼路径；其余用 InstanceLocation + ErrorKind 映射文案。
func formatProposalValidation(ve *jsonschema.ValidationError) string {
	if len(ve.Causes) > 0 {
		parts := make([]string, 0, len(ve.Causes))
		for _, cause := range ve.Causes {
			if s := formatProposalCause(cause); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	return formatProposalCause(ve)
}

func formatProposalCause(c *jsonschema.ValidationError) string {
	loc := strings.Join(c.InstanceLocation, "/")
	if loc == "" {
		loc = "payload"
	}
	switch typed := c.ErrorKind.(type) {
	case *kind.Required:
		return fmt.Sprintf("%s: 缺少必填字段 %s", loc, strings.Join(typed.Missing, "、"))
	case *kind.AdditionalProperties:
		return fmt.Sprintf("%s: 包含未知字段 %s", loc, strings.Join(typed.Properties, "、"))
	case *kind.Type:
		return fmt.Sprintf("%s: 类型错误，应为 %s", loc, strings.Join(typed.Want, "或"))
	case *kind.Enum:
		return fmt.Sprintf("%s: 取值 %v 必须是 %s 之一", loc, typed.Got, joinAny(typed.Want, "或"))
	case *kind.Minimum:
		return fmt.Sprintf("%s: 不能小于 %s", loc, ratString(typed.Want))
	case *kind.Maximum:
		return fmt.Sprintf("%s: 不能大于 %s", loc, ratString(typed.Want))
	case *kind.ExclusiveMinimum:
		return fmt.Sprintf("%s: 必须大于 %s", loc, ratString(typed.Want))
	case *kind.ExclusiveMaximum:
		return fmt.Sprintf("%s: 必须小于 %s", loc, ratString(typed.Want))
	default:
		return fmt.Sprintf("%s: %s 约束不满足", loc, strings.Join(c.ErrorKind.KeywordPath(), "/"))
	}
}

func ratString(r *big.Rat) string {
	if r == nil {
		return ""
	}
	return r.FloatString(0)
}

func joinAny(values []any, sep string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, sep)
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
	// 未知 kind/operation 是模型侧输入偏差，须携带可读 detail 便于自纠，
	// 而非裸 sentinel（Unwrap 链仍满足 ErrorIs 断言）。
	if !resourceKind.Valid() {
		return "", "", "", nil, &domain.InvalidToolArgumentsError{Detail: fmt.Sprintf("不支持的资源类型 %s", resourceKind)}
	}
	if !proposalOperation.Valid() {
		return "", "", "", nil, &domain.InvalidToolArgumentsError{Detail: fmt.Sprintf("不支持的操作 %s", proposalOperation)}
	}
	// 先 normalize 再字段级校验：stringified payload 与嵌套对象统一走
	// proposalPayloadSchemas，payload 内部约束在边界短路而非下沉到服务层。
	if err := validateProposalPayloadSchema(resourceKind, payload); err != nil {
		return "", "", "", nil, err
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
