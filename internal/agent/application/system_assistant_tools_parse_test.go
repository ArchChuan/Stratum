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
