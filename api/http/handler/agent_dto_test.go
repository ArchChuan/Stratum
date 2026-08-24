package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
)

// TestCreateAgentRequestJSONRoundTrip 验证 delegate 三字段的 JSON 契约：前端
// delegateEnabled/delegateMaxDepth/delegateDefaultMaxSteps 透传到
// CreateAgentRequest（HTTP 参数契约由 contract_test.go 守护，这里补字段级绑定）。
func TestCreateAgentRequestJSONRoundTrip(t *testing.T) {
	raw := `{"name":"Alpha","llmModel":"qwen-plus","maxIterations":5,
		"delegateEnabled":true,"delegateMaxDepth":2,"delegateDefaultMaxSteps":7}`
	var req CreateAgentRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &req))
	require.True(t, req.DelegateEnabled)
	require.Equal(t, 2, req.DelegateMaxDepth)
	require.Equal(t, 7, req.DelegateDefaultMaxSteps)
}

func TestUpdateAgentRequestJSONRoundTrip(t *testing.T) {
	raw := `{"name":"Alpha","llmModel":"qwen-plus",
		"delegateEnabled":false,"delegateMaxDepth":1,"delegateDefaultMaxSteps":5}`
	var req UpdateAgentRequest
	require.NoError(t, json.Unmarshal([]byte(raw), &req))
	require.NotNil(t, req.DelegateEnabled)
	require.False(t, *req.DelegateEnabled)
	require.Equal(t, 1, req.DelegateMaxDepth)
	require.Equal(t, 5, req.DelegateDefaultMaxSteps)

	// 缺省字段 → nil:Update 保留现有值,不得把缺省当作显式 false 覆盖。
	var absent UpdateAgentRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"Alpha","llmModel":"qwen-plus"}`), &absent))
	require.Nil(t, absent.DelegateEnabled)
}

// TestDTOToResponseMapsDelegateFields 验证 service AgentDTO → wire AgentResponse
// 的三字段映射不被遗漏（新字段最易在 dtoToResponse 映射时丢）。
func TestDTOToResponseMapsDelegateFields(t *testing.T) {
	dto := agent.AgentDTO{
		ID: "a1", Name: "Alpha",
		DelegateEnabled:         true,
		DelegateMaxDepth:        2,
		DelegateDefaultMaxSteps: 7,
	}
	resp := dtoToResponse(dto)
	require.True(t, resp.DelegateEnabled)
	require.Equal(t, 2, resp.DelegateMaxDepth)
	require.Equal(t, 7, resp.DelegateDefaultMaxSteps)
}
