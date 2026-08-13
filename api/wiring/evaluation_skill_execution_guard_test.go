package wiring

import (
	"context"
	"testing"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/stretchr/testify/require"
)

// noOpSkillBinding 返回 not found,供非 builtin 用例干净走完守卫后的下游路径。
type noOpSkillBinding struct{}

func (noOpSkillBinding) FindAgentBySkill(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// TestAgentScenarioEvaluationAdapterRejectsBuiltinSkill:评测 skill 场景执行入口
// 对内置 skill fail closed。守卫在任何依赖(agents/skills/bindings)之前返回,
// 零字段 adapter 即可验证;ExecuteSkillScenario 不被触达。
func TestAgentScenarioEvaluationAdapterRejectsBuiltinSkill(t *testing.T) {
	adapter := agentScenarioEvaluationAdapter{}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "builtin:platform-guide", RevisionID: "revision-1"}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: map[string]any{"q": "x"}})
	require.ErrorIs(t, err, skilldomain.ErrPlatformManagedSkill)
}

// TestAgentScenarioEvaluationAdapterRejectsBuiltinSkillSuffix:非 builtin 前缀的
// skill 引用不触发守卫(正常路径由依赖驱动,零字段 adapter 返回下游错误即证明
// 守卫放行)。
func TestAgentScenarioEvaluationAdapterRejectsBuiltinSkillSuffix(t *testing.T) {
	adapter := agentScenarioEvaluationAdapter{bindings: noOpSkillBinding{}}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "not-builtin-skill", RevisionID: "revision-1"}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: map[string]any{"q": "x"}})
	require.NotErrorIs(t, err, skilldomain.ErrPlatformManagedSkill)
	require.Error(t, err, "non-builtin skill should pass the guard and fail downstream (no agent bound)")
}
