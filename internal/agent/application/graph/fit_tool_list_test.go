package graph

import (
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
	"github.com/stretchr/testify/require"
)

// TestFitToolList_FullListPreservesDeclarationOrder 验证预算充足时 fitToolList
// 全量返回并保持声明顺序（plan → skill → available），不触发排序。
func TestFitToolList_FullListPreservesDeclarationOrder(t *testing.T) {
	all := append(PlanToolDefinitions(),
		port.ToolDefinition{Name: "skill-a", Description: "a", InputSchema: map[string]any{"type": "object"}},
		port.ToolDefinition{Name: "stratum_continue_reasoning", Description: "r", InputSchema: map[string]any{"type": "object"}},
	)
	encoded, err := json.Marshal(all)
	require.NoError(t, err)
	allowance := tokenutil.EstimateText(string(encoded))

	got := fitToolList(all, allowance)
	require.Equal(t, toolNamesOf(all), toolNamesOf(got), "预算充足必须保持声明顺序全量返回")
}

// TestFitToolList_PrioritizesActivatedCapabilitiesOverPlanTools 守护预算裁剪
// 优先级（spec 第 2 节 tools 配额 + 产品意图"技能激活=功能开关"）：预算不足
// 时激活技能与授权能力工具优先打包，plan 工作流工具最后裁。
func TestFitToolList_PrioritizesActivatedCapabilitiesOverPlanTools(t *testing.T) {
	capabilities := []port.ToolDefinition{
		{Name: "skill-a", Description: "activate a", InputSchema: map[string]any{"type": "object"}},
		{Name: "skill-b", Description: "activate b", InputSchema: map[string]any{"type": "object"}},
		{Name: "stratum_continue_reasoning", Description: "reason", InputSchema: map[string]any{"type": "object"}},
	}
	all := append(PlanToolDefinitions(), capabilities...)
	encoded, err := json.Marshal(capabilities)
	require.NoError(t, err)
	// allowance 只够能力工具自身：plan 工具必须全部让位。
	allowance := tokenutil.EstimateText(string(encoded))

	got := fitToolList(all, allowance)
	require.ElementsMatch(t, toolNamesOf(capabilities), toolNamesOf(got),
		"预算不足时必须先保激活技能/授权能力工具、裁 plan 工作流工具")
}

func toolNamesOf(tools []port.ToolDefinition) []string {
	names := make([]string, len(tools))
	for i := range tools {
		names[i] = tools[i].Name
	}
	return names
}
