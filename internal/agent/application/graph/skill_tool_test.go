package graph_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func skillCatalogFixture() map[string]port.SkillActivation {
	return map[string]port.SkillActivation{
		"skill-a": {SkillID: "skill-a", Name: "skill-a", RevisionID: "rev-a", Description: "desc A"},
		"skill-b": {SkillID: "skill-b", Name: "skill-b", RevisionID: "rev-b", Description: "desc B"},
	}
}

func TestBuildSkillTool_EmptyCatalogOmitsTool(t *testing.T) {
	// 空 catalog：不暴露空描述的统一工具（Spec D1）。
	require.Nil(t, graph.BuildSkillToolForTest(nil, nil, 1000))
	require.Nil(t, graph.BuildSkillToolForTest(map[string]port.SkillActivation{}, nil, 1000))
}

func TestBuildSkillTool_DescriptionListsCatalogWithActiveMarker(t *testing.T) {
	tool := graph.BuildSkillToolForTest(skillCatalogFixture(), []port.SkillActivation{
		{SkillID: "skill-b", Name: "skill-b", RevisionID: "rev-b", Instructions: "INST B"},
	}, 100000)

	require.NotNil(t, tool)
	require.Equal(t, "stratum_skill", tool.Name)
	require.Equal(t, domain.ProviderTypeSkill, tool.ProviderType)
	require.Contains(t, tool.Description, "- skill-a: desc A")
	// marker 紧跟 name 之后、冒号之前（buildSkillCatalogLines 的 "- %s%s: %s" 格式）。
	require.Contains(t, tool.Description, "- skill-b (已激活): desc B")
	require.NotContains(t, tool.Description, "skill-a (已激活)")
}

func TestBuildSkillTool_TruncatesDescriptionToAllowance(t *testing.T) {
	catalog := map[string]port.SkillActivation{
		"skill-a": {SkillID: "skill-a", Name: "skill-a", RevisionID: "rev-a", Description: "这是很长很长的描述" + repeat("内容填充", 200)},
		"skill-b": {SkillID: "skill-b", Name: "skill-b", RevisionID: "rev-b", Description: "这是很长很长的描述" + repeat("内容填充", 200)},
	}
	// 预算 >= 空描述工具的固定 JSON 开销下限（约 45 tokens）时，两阶段截断保证
	// 工具实际编码恒 fit allowance，stratum_skill 在 fitToolList 贪心打包中永不被整丢。
	for _, allowance := range []int{60, 200, 1000, 100000} {
		tool := graph.BuildSkillToolForTest(catalog, nil, allowance)
		require.NotNil(t, tool, "allowance=%d: 工具永不被整丢", allowance)
		encoded, err := json.Marshal(tool)
		require.NoError(t, err)
		require.LessOrEqual(t, tokenutil.EstimateText(string(encoded)), allowance,
			"allowance=%d: stratum_skill 编码必须 fit allowance", allowance)
	}
}

func TestBuildSkillTool_BelowOverheadFloorKeepsToolWithEmptyDescription(t *testing.T) {
	// 极端小预算（低于空描述工具的固定 JSON 开销）下物理上无法 fit 编码，工具保持
	// 存在、描述压至空。此边界是 fitToolList 唯一可能整丢的残余场景，属可接受风险。
	tool := graph.BuildSkillToolForTest(skillCatalogFixture(), nil, 8)
	require.NotNil(t, tool, "低于固定开销下限也不得整丢")
	require.Empty(t, tool.Description)
}

func TestBuildSkillTool_ZeroAllowanceKeepsFullListing(t *testing.T) {
	// allowance<=0 表示预算未初始化（fitToolsToContextBudget 早退），返回全文。
	tool := graph.BuildSkillToolForTest(skillCatalogFixture(), nil, 0)
	require.NotNil(t, tool)
	require.Contains(t, tool.Description, "- skill-a: desc A")
	require.Contains(t, tool.Description, "- skill-b: desc B")
}

func TestBuildReActGraph_StratumSkillUnknownSkillReturnsError(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "x1", Name: "stratum_skill", Arguments: map[string]any{"skill": "skill-ghost"}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	out, err := cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "activate"}},
		AvailableTools: []port.ToolDefinition{{Name: "mcp:orders:get", ProviderType: "mcp"}},
		SkillCatalog:   skillCatalogFixture(),
	}, graph.RunConfig[graph.ReActState]{MaxSteps: 8})
	require.NoError(t, err)

	require.Empty(t, out.Actives, "未知 skill 不得激活")
	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, domain.ToolTraceStatusError, out.ToolObservations[0].Status)
	require.Contains(t, out.ToolObservations[0].RawText, `unknown skill "skill-ghost"`)
	require.Contains(t, out.ToolObservations[0].RawText, "skill-a")
}

func TestBuildReActGraph_StratumSkillMissingArgumentReturnsError(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "x1", Name: "stratum_skill", Arguments: map[string]any{}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	out, err := cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "activate"}},
		AvailableTools: []port.ToolDefinition{{Name: "mcp:orders:get", ProviderType: "mcp"}},
		SkillCatalog:   skillCatalogFixture(),
	}, graph.RunConfig[graph.ReActState]{MaxSteps: 8})
	require.NoError(t, err)

	require.Empty(t, out.Actives)
	require.Len(t, out.ToolObservations, 1)
	require.Equal(t, domain.ToolTraceStatusError, out.ToolObservations[0].Status)
}

func TestBuildReActGraph_StratumSkillRecordsPerSkillProviderAttribution(t *testing.T) {
	// 统一 stratum_skill 调用按参数 skill 恢复逐 skill 观测归因（classifySkillProvider），
	// 而非统一工具本身的通用引用（capability id = skill id、node = 解析名）。
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "x1", Name: "stratum_skill", Arguments: map[string]any{"skill": "skill-a"}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	out, err := cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "activate"}},
		AvailableTools: []port.ToolDefinition{{Name: "mcp:orders:get", ProviderType: "mcp"}},
		SkillCatalog: map[string]port.SkillActivation{
			"skill-a": {SkillID: "skill-a", Name: "skill-a", RevisionID: "rev-a", Instructions: "INST A"},
		},
	}, graph.RunConfig[graph.ReActState]{MaxSteps: 8})
	require.NoError(t, err)

	require.Len(t, out.ToolObservations, 1)
	obs := out.ToolObservations[0]
	require.Equal(t, domain.ProviderTypeSkill, obs.ProviderType)
	require.Equal(t, domain.ProviderTypeSkill, obs.ToolType)
	require.Equal(t, "skill-a", obs.ProviderID)
	require.Equal(t, "skill-a", obs.CapabilityID)

	// trace 事件节点归因到具体 skill 解析名，而非统一工具名。
	found := false
	for _, ev := range out.TraceEvents {
		if ev.NodeID == "skill-a" && ev.NodeType == domain.ObservationTypeSkill {
			found = true
			break
		}
	}
	require.True(t, found, "trace 事件应归因到 skill 节点")
}

func TestBuildReActGraph_StratumSkillActivationReturnsLocationGuide(t *testing.T) {
	stub := &capGWSequence{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "x1", Name: "stratum_skill", Arguments: map[string]any{"skill": "skill-a"}}}},
		{Content: "done"},
	}}
	cg, err := graph.BuildReActGraph(stub, graph.NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	out, err := cg.Invoke(context.Background(), graph.ReActState{
		Model: "qwen", Messages: []port.LLMMessage{{Role: "user", Content: "activate"}},
		AvailableTools: []port.ToolDefinition{{Name: "mcp:orders:get", ProviderType: "mcp"}},
		SkillCatalog: map[string]port.SkillActivation{
			"skill-a": {SkillID: "skill-a", Name: "skill-a", RevisionID: "rev-a", Instructions: "INST A"},
		},
	}, graph.RunConfig[graph.ReActState]{MaxSteps: 8})
	require.NoError(t, err)

	require.Len(t, out.Actives, 1)
	require.Equal(t, "skill-a", out.Actives[0].SkillID)
	// 工具结果返回注入位置指引（Spec D2），指向下一轮 system 注入的标题。
	require.Len(t, out.ToolObservations, 1)
	require.Contains(t, out.ToolObservations[0].RawText, "Skill skill-a (revision rev-a) 已激活")
	require.Contains(t, out.ToolObservations[0].RawText, "Active Skill skill-a (revision rev-a)")
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
