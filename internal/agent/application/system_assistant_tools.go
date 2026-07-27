package application

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	ToolSearchOfficialDocs    = "stratum_search_official_docs"
	ToolDiagnoseTenant        = "stratum_diagnose_tenant"
	ToolProposeResourceChange = "stratum_propose_resource_change"
)

var ErrInvalidSystemAssistantToolArguments = errors.New("invalid system assistant tool arguments")

func SystemAssistantToolDefinitions() []port.ToolDefinition {
	return []port.ToolDefinition{
		{
			Name: ToolSearchOfficialDocs, ProviderType: domain.ProviderTypeInternal,
			Description: "检索当前版本的 Stratum 官方文档。仅在需要回答平台使用方式时调用。",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": constants.SystemAssistantQueryMaxRunes}},
				"required":   []string{"query"},
			},
		},
		{
			Name: ToolDiagnoseTenant, ProviderType: domain.ProviderTypeInternal,
			Description: "按当前登录成员权限只读诊断当前租户的应用状态。",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"areas": map[string]any{
					"type": "array", "minItems": 1, "maxItems": constants.SystemAssistantAreasMaxCount, "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": []string{"agent", "skill", "mcp", "knowledge", "model"}},
				}},
				"required": []string{"areas"},
			},
		},
	}
}

func SystemAssistantToolDefinitionsForRole(roleClass string) []port.ToolDefinition {
	tools := SystemAssistantToolDefinitions()
	if roleClass != "admin" && roleClass != "owner" {
		return tools
	}
	return append(tools, port.ToolDefinition{
		Name: ToolProposeResourceChange, ProviderType: domain.ProviderTypeInternal,
		Description: "创建受治理的资源变更提案。只生成待审提案，不直接修改资源。",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"resourceKind": map[string]any{"type": "string", "enum": []string{"agent", "skill_draft", "mcp_config", "knowledge_workspace"}},
				"operation":    map[string]any{"type": "string", "enum": []string{"create", "update"}},
				"resourceId":   map[string]any{"type": "string"},
				"payload":      map[string]any{"type": "object"},
			},
			"required": []string{"resourceKind", "operation", "payload"},
		},
	})
}

func parseProposalArguments(args map[string]any) (domain.ResourceKind, domain.ProposalOperation, string, []byte, error) {
	allowed := map[string]bool{"resourceKind": true, "operation": true, "resourceId": true, "payload": true}
	for key := range args {
		if !allowed[key] {
			return "", "", "", nil, fmt.Errorf("%w: unknown field %s", ErrInvalidSystemAssistantToolArguments, key)
		}
	}
	kind, kindOK := args["resourceKind"].(string)
	operation, operationOK := args["operation"].(string)
	payload, payloadOK := args["payload"].(map[string]any)
	resourceID, _ := args["resourceId"].(string)
	if !kindOK || !operationOK || !payloadOK {
		return "", "", "", nil, ErrInvalidSystemAssistantToolArguments
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", "", "", nil, ErrInvalidSystemAssistantToolArguments
	}
	resourceKind := domain.ResourceKind(kind)
	proposalOperation := domain.ProposalOperation(operation)
	if !resourceKind.Valid() || !proposalOperation.Valid() {
		return "", "", "", nil, ErrInvalidSystemAssistantToolArguments
	}
	return resourceKind, proposalOperation, resourceID, raw, nil
}

func parseOfficialDocsArguments(args map[string]any) (string, error) {
	query, err := domain.ParseOfficialDocsToolArguments(args)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSystemAssistantToolArguments, err)
	}
	return query, nil
}

func parseDiagnosticArguments(args map[string]any) ([]domain.DiagnosticArea, error) {
	areas, err := domain.ParseDiagnosticToolArguments(args)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSystemAssistantToolArguments, err)
	}
	return areas, nil
}
