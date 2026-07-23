package application

import (
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	ToolSearchOfficialDocs = "stratum_search_official_docs"
	ToolDiagnoseTenant     = "stratum_diagnose_tenant"
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
