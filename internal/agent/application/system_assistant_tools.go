package application

import (
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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
				"properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1}},
				"required":   []string{"query"},
			},
		},
		{
			Name: ToolDiagnoseTenant, ProviderType: domain.ProviderTypeInternal,
			Description: "按当前登录成员权限只读诊断当前租户的应用状态。",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"areas": map[string]any{
					"type": "array", "minItems": 1, "uniqueItems": true,
					"items": map[string]any{"type": "string", "enum": []string{"agent", "skill", "mcp", "knowledge", "model"}},
				}},
				"required": []string{"areas"},
			},
		},
	}
}

func parseOfficialDocsArguments(args map[string]any) (string, error) {
	if len(args) != 1 {
		return "", ErrInvalidSystemAssistantToolArguments
	}
	query, ok := args["query"].(string)
	query = strings.TrimSpace(query)
	if !ok || query == "" {
		return "", ErrInvalidSystemAssistantToolArguments
	}
	return query, nil
}

func parseDiagnosticArguments(args map[string]any) ([]domain.DiagnosticArea, error) {
	if len(args) != 1 {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	raw, ok := args["areas"]
	if !ok {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, ErrInvalidSystemAssistantToolArguments
	}
	areas := make([]domain.DiagnosticArea, 0, len(items))
	seen := make(map[domain.DiagnosticArea]struct{}, len(items))
	for _, item := range items {
		value, ok := item.(string)
		area := domain.DiagnosticArea(value)
		if !ok || !area.Valid() {
			return nil, fmt.Errorf("%w: area", ErrInvalidSystemAssistantToolArguments)
		}
		if _, duplicate := seen[area]; duplicate {
			continue
		}
		seen[area] = struct{}{}
		areas = append(areas, area)
	}
	return areas, nil
}
