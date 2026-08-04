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
	ToolSearchOfficialDocs    = domain.SystemAssistantToolSearchOfficialDocs
	ToolDiagnoseTenant        = domain.SystemAssistantToolDiagnoseTenant
	ToolProposeResourceChange = domain.SystemAssistantToolProposeResourceChange
)

var ErrInvalidSystemAssistantToolArguments = errors.New("invalid system assistant tool arguments")

func SystemAssistantToolDefinitions() []port.ToolDefinition {
	return []port.ToolDefinition{
		{
			Name: ToolSearchOfficialDocs, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolSearchOfficialDocs, CapabilityID: ToolSearchOfficialDocs,
			Description: "检索当前版本的 Stratum 官方文档。仅在需要回答平台使用方式时调用。",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": constants.SystemAssistantQueryMaxRunes}},
				"required":   []string{"query"},
			},
		},
		{
			Name: ToolDiagnoseTenant, ProviderType: domain.ProviderTypeInternal,
			ProviderID: ToolDiagnoseTenant, CapabilityID: ToolDiagnoseTenant,
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
		ProviderID: ToolProposeResourceChange, CapabilityID: ToolProposeResourceChange,
		Description: "创建受治理的资源变更提案。只生成待审提案，不直接修改资源。",
		InputSchema: proposalToolSchema(),
	})
}

func proposalToolSchema() map[string]any {
	payloads := map[domain.ResourceKind]map[string]any{
		domain.ResourceAgent: {
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"name":             map[string]any{"type": "string", "minLength": 1},
				"description":      map[string]any{"type": "string"},
				"model":            map[string]any{"type": "string"},
				"maxIterations":    map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"maxContextTokens": map[string]any{"type": "integer", "minimum": 1},
				"skillIds":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
				"mcpToolIds":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
				"workspaceIds":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
			},
			"required": []string{"name", "description", "model", "maxIterations", "maxContextTokens"},
		},
		domain.ResourceSkillDraft: {
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"name":         map[string]any{"type": "string", "minLength": 1},
				"description":  map[string]any{"type": "string"},
				"instructions": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"name", "description", "instructions"},
		},
		domain.ResourceMCPConfig: {
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"name":         map[string]any{"type": "string", "minLength": 1},
				"version":      map[string]any{"type": "string"},
				"transport":    map[string]any{"type": "string", "enum": []string{"stdio", "streamable-http"}},
				"command":      map[string]any{"type": "string"},
				"args":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"url":          map[string]any{"type": "string"},
				"capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
				"timeoutSec": map[string]any{
					"type": "integer", "minimum": minProposalMCPTimeoutSec, "maximum": maxProposalMCPTimeoutSec,
				},
				"retry": proposalRetrySchema(),
			},
			"required": []string{"name", "version", "transport", "timeoutSec"},
		},
		domain.ResourceKnowledgeWorkspace: {
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"name":           map[string]any{"type": "string", "minLength": 1},
				"description":    map[string]any{"type": "string", "minLength": 1},
				"embeddingModel": map[string]any{"type": "string"},
			},
			"required": []string{"name", "description", "embeddingModel"},
		},
	}
	kinds := []domain.ResourceKind{
		domain.ResourceAgent, domain.ResourceSkillDraft, domain.ResourceMCPConfig, domain.ResourceKnowledgeWorkspace,
	}
	branches := make([]any, 0, len(kinds)*2)
	for _, kind := range kinds {
		for _, operation := range []domain.ProposalOperation{domain.OperationCreate, domain.OperationUpdate} {
			properties := map[string]any{
				"resourceKind": map[string]any{"const": string(kind)},
				"operation":    map[string]any{"const": string(operation)},
				"payload":      payloads[kind],
			}
			required := []string{"resourceKind", "operation", "payload"}
			if operation == domain.OperationUpdate {
				properties["resourceId"] = map[string]any{"type": "string", "minLength": 1}
				required = append(required, "resourceId")
			}
			branches = append(branches, map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": properties, "required": required,
			})
		}
	}
	return map[string]any{"oneOf": branches}
}

func proposalRetrySchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"maxRetries": map[string]any{
				"type": "integer", "minimum": minProposalMCPRetryCount, "maximum": maxProposalMCPRetryCount,
			},
			"initialDelayMs": map[string]any{
				"type": "integer", "minimum": minProposalMCPRetryInitialDelayMs,
				"maximum": maxProposalMCPRetryInitialDelayMs,
			},
			"maxDelayMs": map[string]any{
				"type": "integer", "minimum": minProposalMCPRetryMaxDelayMs,
				"maximum": maxProposalMCPRetryMaxDelayMs,
			},
			"backoffFactor": map[string]any{
				"type": "number", "minimum": minProposalMCPRetryBackoffFactor,
				"maximum": maxProposalMCPRetryBackoffFactor,
			},
		},
		"required": []string{"enabled", "maxRetries", "initialDelayMs", "maxDelayMs", "backoffFactor"},
	}
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
