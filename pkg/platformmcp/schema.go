package platformmcp

import "github.com/byteBuilderX/stratum/pkg/constants"

func InputSchema(toolName string) (map[string]any, bool) {
	switch toolName {
	case ToolSearchOfficialDocs:
		return closedObject(map[string]any{"query": map[string]any{
			"type": "string", "minLength": 1, "maxLength": constants.SystemAssistantQueryMaxRunes,
		}}, []string{"query"}), true
	case ToolDiagnoseTenant:
		return closedObject(map[string]any{"areas": map[string]any{
			"type": "array", "minItems": 1, "maxItems": constants.SystemAssistantAreasMaxCount,
			"uniqueItems": true, "items": map[string]any{
				"type": "string", "enum": jsonStringArray("agent", "skill", "mcp", "knowledge", "model"),
			},
		}}, []string{"areas"}), true
	case ToolProposeResourceChange:
		return proposalSchema(), true
	default:
		return nil, false
	}
}

func proposalSchema() map[string]any {
	payloads := proposalPayloadSchemas()
	kinds := []string{"agent", "skill_draft", "mcp_config", "knowledge_workspace"}
	branches := make([]any, 0, len(kinds)*2)
	for _, kind := range kinds {
		branches = append(branches,
			proposalBranch(kind, "create", payloads[kind]),
			proposalBranch(kind, "update", payloads[kind]),
		)
	}
	return map[string]any{"type": "object", "oneOf": branches}
}

func proposalBranch(kind, operation string, payload map[string]any) map[string]any {
	properties := map[string]any{
		"resourceKind": map[string]any{"const": kind},
		"operation":    map[string]any{"const": operation},
		"payload":      payload,
	}
	required := []string{"resourceKind", "operation", "payload"}
	if operation == "update" {
		properties["resourceId"] = map[string]any{"type": "string", "minLength": 1}
		required = append(required, "resourceId")
	}
	return closedObject(properties, required)
}

func proposalPayloadSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"agent": closedObject(map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1}, "description": map[string]any{"type": "string"},
			"model": map[string]any{"type": "string"}, "maxIterations": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			"maxContextTokens": map[string]any{"type": "integer", "minimum": 1},
			"skillIds":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
			"mcpToolIds":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
			"workspaceIds":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
		}, []string{"name", "description", "model", "maxIterations", "maxContextTokens"}),
		"skill_draft": closedObject(map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1}, "description": map[string]any{"type": "string"},
			"instructions": map[string]any{"type": "string", "minLength": 1},
		}, []string{"name", "description", "instructions"}),
		"mcp_config": closedObject(map[string]any{
			"name": map[string]any{"type": "string", "minLength": 1}, "version": map[string]any{"type": "string"},
			"transport": map[string]any{"type": "string", "enum": jsonStringArray("stdio", "streamable-http")},
			"command":   map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"url": map[string]any{"type": "string"}, "capabilities": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
			"timeoutSec": map[string]any{"type": "integer", "minimum": 1, "maximum": 300}, "retry": proposalRetrySchema(),
		}, []string{"name", "version", "transport", "timeoutSec"}),
		"knowledge_workspace": closedObject(map[string]any{
			"name":        map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string", "minLength": 1}, "embeddingModel": map[string]any{"type": "string"},
		}, []string{"name", "description", "embeddingModel"}),
	}
}

func proposalRetrySchema() map[string]any {
	return closedObject(map[string]any{
		"enabled": map[string]any{"type": "boolean"}, "maxRetries": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		"initialDelayMs": map[string]any{"type": "integer", "minimum": 100, "maximum": 60000},
		"maxDelayMs":     map[string]any{"type": "integer", "minimum": 1000, "maximum": 300000},
		"backoffFactor":  map[string]any{"type": "number", "minimum": 1.0, "maximum": 10.0},
	}, []string{"enabled", "maxRetries", "initialDelayMs", "maxDelayMs", "backoffFactor"})
}

func closedObject(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "properties": properties,
		"required": jsonStringArray(required...),
	}
}

func jsonStringArray(values ...string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}
