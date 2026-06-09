package capgateway_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
)

func TestCapabilityRequestValidate_LLMMissingLLM(t *testing.T) {
	req := capgateway.CapabilityRequest{
		TraceID:  "t1",
		TenantID: "tenant1",
		Type:     capgateway.CapLLM,
		Timeout:  10 * time.Second,
	}
	err := req.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "LLM")
}

func TestCapabilityRequestValidate_SkillMissingSkill(t *testing.T) {
	req := capgateway.CapabilityRequest{
		TraceID:  "t2",
		TenantID: "tenant1",
		Type:     capgateway.CapSkill,
		Timeout:  10 * time.Second,
	}
	require.Error(t, req.Validate())
}

func TestCapabilityRequestValidate_Valid(t *testing.T) {
	req := capgateway.CapabilityRequest{
		TraceID:  "t3",
		TenantID: "tenant1",
		Type:     capgateway.CapLLM,
		Timeout:  10 * time.Second,
		LLM: &capgateway.LLMCapRequest{
			Model:    "qwen-turbo",
			Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}},
		},
	}
	require.NoError(t, req.Validate())
}

func TestToolDefinitionJSONRoundTrip(t *testing.T) {
	td := capgateway.ToolDefinition{
		Name:        "get_weather",
		Description: "Get weather info",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	b, err := json.Marshal(td)
	require.NoError(t, err)
	var out capgateway.ToolDefinition
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, td.Name, out.Name)
}
