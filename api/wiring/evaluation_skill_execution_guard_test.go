package wiring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
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

// ctxCaptureSkillBinding 捕获 FindAgentBySkill 收到的 ctx tenant，供 D7 评测执行
// 的 tenant 传播断言（后台 worker 路径 ctx 本无 tenant，adapter 顶层必须包裹）。
type ctxCaptureSkillBinding struct {
	agentID  string
	tenantID string
	userID   string
}

func (b *ctxCaptureSkillBinding) FindAgentBySkill(ctx context.Context, _ string) (string, bool, error) {
	if tc, ok := postgres.FromContext(ctx); ok {
		b.tenantID, b.userID = tc.TenantID, tc.UserID
	}
	return b.agentID, true, nil
}

// ctxCaptureSkillResolver 捕获 ResolveSkills 收到的 ctx tenant：publishedSkillActivationResolver
// 忽略 tenantID 参数、只依赖 ctx tenant，缺失即 skill repo 经 execTenant fail-closed。
type ctxCaptureSkillResolver struct {
	tenantID string
}

func (r *ctxCaptureSkillResolver) ResolveSkills(
	ctx context.Context, _ string, refs []agentport.SkillRevisionRef,
) (map[string]agentport.SkillActivation, error) {
	if tc, ok := postgres.FromContext(ctx); ok {
		r.tenantID = tc.TenantID
	}
	catalog := make(map[string]agentport.SkillActivation, len(refs))
	for _, ref := range refs {
		catalog[ref.SkillID] = agentport.SkillActivation{SkillID: ref.SkillID, RevisionID: ref.RevisionID}
	}
	return catalog, nil
}

// TestAgentScenarioEvaluationPropagatesTenantThroughSkillFlow 验证 D7 评测后台
// worker 路径（ctx 本无 tenant）执行 skill 场景评测时，adapter 顶层一次性把 tenant
// 包裹进 ctx 并贯穿 skill 绑定解析（FindAgentBySkill）与激活解析（ResolveSkills）。
// 防回归护栏：若 ExecuteRevision 顶层删掉 WithTenant（只靠 resolvePinnedAgent 局部
// 包裹），ResolveSkills 会收到未包裹 ctx，本测试在 skills 断言处失败。执行层注入最小
// AgentService（无 model validator），模型校验按预期 fail-closed——证明执行已触达
// agent 层而非早期传播失败。
func TestAgentScenarioEvaluationPropagatesTenantThroughSkillFlow(t *testing.T) {
	rev := agentdomain.AgentRevision{
		AgentID: "agent-1", Type: agentdomain.ReActAgent, SystemPrompt: "你是助手", Model: "qwen-max",
		MaxIterations:   3,
		ModelParameters: agentdomain.ModelParameters{MaxContextTokens: 8192, MaxTokens: 2048},
	}
	bindings := &ctxCaptureSkillBinding{agentID: "agent-1"}
	skills := &ctxCaptureSkillResolver{}
	adapter := agentScenarioEvaluationAdapter{
		agents:    agentapp.NewAgentService(agentapp.AgentServiceDeps{}),
		revisions: &fakeAgentRevisionSvc{found: true, revision: rev},
		bindings:  bindings,
		skills:    skills,
	}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}
	ctx := evaldomain.WithEvalSnapshot(context.Background(), &evaldomain.EvaluationContextSnapshot{
		PinnedAssignments: evaldomain.PinnedAssignments{
			SkillAgentRevision: map[string]string{ref.ResourceID: "pinned-rev-1"},
		},
	})
	_, err := adapter.ExecuteRevision(ctx, "tenant-1", "user-1", ref, evaldomain.EvalCase{Input: "hi"})
	require.Equal(t, "tenant-1", bindings.tenantID, "FindAgentBySkill ctx must carry tenant")
	require.Equal(t, "user-1", bindings.userID, "FindAgentBySkill ctx must carry requesting user")
	require.Equal(t, "tenant-1", skills.tenantID, "ResolveSkills ctx must carry tenant")
	require.Error(t, err)
	require.Contains(t, err.Error(), "assistant model")
}
