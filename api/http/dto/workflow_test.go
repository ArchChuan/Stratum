package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto"
	"github.com/stretchr/testify/require"
)

func TestStartWorkflowRunRequestBuildsFlatDomainInput(t *testing.T) {
	var request dto.StartWorkflowRunRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"version_id":"version-1","task":"分析市场","fields":{"region":"east","count":3},"idempotency_key":"key-1"
	}`), &request))
	input, err := request.RunInput()
	require.NoError(t, err)
	require.Equal(t, "分析市场", input["task"])
	require.Equal(t, "east", input["region"])
	require.Equal(t, float64(3), input["count"])
}

func TestStartWorkflowRunRequestRejectsReservedTaskField(t *testing.T) {
	request := dto.StartWorkflowRunRequest{Task: "真实任务", Fields: map[string]any{"task": "覆盖"}}
	_, err := request.RunInput()
	require.Error(t, err)
}
