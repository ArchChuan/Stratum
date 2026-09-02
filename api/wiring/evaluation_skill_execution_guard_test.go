package wiring

import (
	"context"
	"testing"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/stretchr/testify/require"
)

// noOpSkillBinding 返回 not found：评测链路对内置/普通 skill 一视同仁，
// 不再做 builtin 前缀特判，统一经 FindAgentBySkill 解析到绑定 agent。
type noOpSkillBinding struct{}

func (noOpSkillBinding) FindAgentBySkill(context.Context, string) (string, bool, error) {
	return "", false, nil
}

// TestAgentScenarioEvaluationAdapterRoutesBuiltinSkillLikeNormal:内置 skill
// 不再被 builtin 守卫拦截——与普通 skill 一样走 FindAgentBySkill，无绑定 agent
// 时返回相同的下游错误（fail 于缺 agent，而非平台特判 sentinel）。
// D7 后执行要求 run 快照中已 pin 承载 agent revision（缺失 → 提示 recreate run）。
func TestAgentScenarioEvaluationAdapterRoutesBuiltinSkillLikeNormal(t *testing.T) {
	for _, ref := range []evaldomain.ResourceRef{
		{Kind: evaldomain.ResourceKindSkill, ResourceID: "builtin:platform-guide", RevisionID: "revision-1"},
		{Kind: evaldomain.ResourceKindSkill, ResourceID: "not-builtin-skill", RevisionID: "revision-1"},
	} {
		adapter := agentScenarioEvaluationAdapter{bindings: noOpSkillBinding{}, revisions: &fakeAgentRevisionSvc{}}
		ctx := evaldomain.WithEvalSnapshot(context.Background(), &evaldomain.EvaluationContextSnapshot{
			PinnedAssignments: evaldomain.PinnedAssignments{
				SkillAgentRevision: map[string]string{ref.ResourceID: "pinned-rev-1"},
			},
		})
		_, err := adapter.ExecuteRevision(ctx, "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: map[string]any{"q": "x"}})
		require.ErrorContains(t, err, "requires an Agent bound to Skill")
	}
}

// TestAgentScenarioEvaluationAdapterFailsClosedWithoutPinnedRevision 验证 D7
// fail-closed：run 快照缺失或未 pin 承载 agent revision 时，执行拒绝并提示
// recreate run，绝不落到 Registry 当前生产配置。
func TestAgentScenarioEvaluationAdapterFailsClosedWithoutPinnedRevision(t *testing.T) {
	adapter := agentScenarioEvaluationAdapter{bindings: noOpSkillBinding{}, revisions: &fakeAgentRevisionSvc{}}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: "hi"})
	require.ErrorContains(t, err, "evaluation context snapshot required")

	ctx := evaldomain.WithEvalSnapshot(context.Background(), &evaldomain.EvaluationContextSnapshot{
		PinnedAssignments: evaldomain.PinnedAssignments{SkillAgentRevision: map[string]string{}},
	})
	_, err = adapter.ExecuteRevision(ctx, "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: "hi"})
	require.ErrorContains(t, err, "no pinned agent revision")
	require.ErrorContains(t, err, "recreate the run")
}
