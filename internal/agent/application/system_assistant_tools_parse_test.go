package application

import (
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
)

// validAgentCreateArgs is the canonical nested-object payload for an agent
// create call, as produced by a model that honors the strict nested-object
// schema.
func validAgentCreateArgs() map[string]any {
	return map[string]any{
		"resourceKind": "agent",
		"operation":    "create",
		"payload": map[string]any{
			"name":             "智能助手",
			"description":      "通用对话助手",
			"model":            "glm-4.5",
			"maxIterations":    10,
			"maxContextTokens": 4096,
		},
	}
}

func TestParseResourceChangeToolArgumentsAcceptsNestedPayload(t *testing.T) {
	kind, operation, resourceID, raw, err := ParseResourceChangeToolArguments(validAgentCreateArgs())
	require.NoError(t, err)
	require.Equal(t, domain.ResourceAgent, kind)
	require.Equal(t, domain.OperationCreate, operation)
	require.Empty(t, resourceID)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "智能助手", decoded["name"])
}

// TestParseResourceChangeToolArgumentsAcceptsStringifiedPayload guards against
// models that serialize the strict nested payload object as a JSON string
// (observed with glm-5 in production: the tool call arrives with
// payload="{\"name\": ...}"). The parser must tolerate this boundary variance
// instead of failing every resource-change call.
func TestParseResourceChangeToolArgumentsAcceptsStringifiedPayload(t *testing.T) {
	rawPayload, err := json.Marshal(validAgentCreateArgs()["payload"])
	require.NoError(t, err)
	args := map[string]any{
		"resourceKind": "agent",
		"operation":    "create",
		"payload":      string(rawPayload),
	}
	kind, operation, _, payload, err := ParseResourceChangeToolArguments(args)
	require.NoError(t, err)
	require.Equal(t, domain.ResourceAgent, kind)
	require.Equal(t, domain.OperationCreate, operation)
	require.JSONEq(t, string(rawPayload), string(payload))
}

func TestParseResourceChangeToolArgumentsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "unknown field", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": map[string]any{}, "extra": true}},
		{name: "missing payload", args: map[string]any{"resourceKind": "agent", "operation": "create"}},
		{name: "payload empty string", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": ""}},
		{name: "payload malformed string", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": "{not-json"}},
		{name: "invalid resource kind", args: map[string]any{"resourceKind": "bogus", "operation": "create", "payload": map[string]any{}}},
		{name: "invalid operation", args: map[string]any{"resourceKind": "agent", "operation": "bogus", "payload": map[string]any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := ParseResourceChangeToolArguments(tt.args)
			require.ErrorIs(t, err, domain.ErrInvalidSystemAssistantToolArguments)
		})
	}
}

func TestParseResourceChangeToolArgumentsUpdateRequiresResourceID(t *testing.T) {
	kind, operation, resourceID, _, err := ParseResourceChangeToolArguments(map[string]any{
		"resourceKind": "mcp_config",
		"operation":    "update",
		"resourceId":   "mcp-1",
		"payload":      map[string]any{"name": "server", "version": "1", "transport": "streamable-http", "timeoutSec": 30},
	})
	require.NoError(t, err)
	require.Equal(t, domain.ResourceMCPConfig, kind)
	require.Equal(t, domain.OperationUpdate, operation)
	require.Equal(t, "mcp-1", resourceID)
}

// TestParseResourceChangeToolArgumentsFieldValidationSurfacesReadableDetail
// guards the field-level schema validation: failures must surface as a
// *domain.InvalidToolArgumentsError carrying a model-readable Chinese detail
// (so the model can self-correct) rather than a bare sentinel error.
func TestParseResourceChangeToolArgumentsFieldValidationSurfacesReadableDetail(t *testing.T) {
	validAgent := map[string]any{"name": "a", "description": "d", "model": "m", "maxIterations": 3, "maxContextTokens": 100}
	tests := []struct {
		name   string
		args   map[string]any
		detail string
	}{
		{name: "missing required", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": map[string]any{}}, detail: "缺少必填字段"},
		{name: "unknown payload field", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": func() map[string]any {
			m := map[string]any{}
			for k, v := range validAgent {
				m[k] = v
			}
			m["extraField"] = true
			return m
		}()}, detail: "未知字段"},
		{name: "maxIterations out of range", args: map[string]any{"resourceKind": "agent", "operation": "create", "payload": map[string]any{"name": "a", "description": "d", "model": "m", "maxIterations": 99, "maxContextTokens": 100}}, detail: "不能大于"},
		{name: "mcp transport enum", args: map[string]any{"resourceKind": "mcp_config", "operation": "create", "payload": map[string]any{"name": "s", "version": "1", "transport": "weird", "timeoutSec": 30}}, detail: "必须"},
		{name: "unknown kind short circuit", args: map[string]any{"resourceKind": "bogus", "operation": "create", "payload": map[string]any{}}, detail: "不支持的资源类型"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, _, err := ParseResourceChangeToolArguments(tc.args)
			var iva *domain.InvalidToolArgumentsError
			require.ErrorAs(t, err, &iva)
			require.Contains(t, iva.Detail, tc.detail)
		})
	}
}

// TestParseResourceChangeToolArgumentsAcceptsSystemPrompt guards that the
// optional systemPrompt field is accepted by the boundary validator (it is
// the direction-A create path input).
func TestParseResourceChangeToolArgumentsAcceptsSystemPrompt(t *testing.T) {
	args := map[string]any{
		"resourceKind": "agent",
		"operation":    "create",
		"payload": map[string]any{
			"name": "a", "description": "d", "model": "m", "maxIterations": 3, "maxContextTokens": 100,
			"systemPrompt": "你是销售助手，只讲中文。",
		},
	}
	kind, _, _, raw, err := ParseResourceChangeToolArguments(args)
	require.NoError(t, err)
	require.Equal(t, domain.ResourceAgent, kind)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "你是销售助手，只讲中文。", decoded["systemPrompt"])
}
