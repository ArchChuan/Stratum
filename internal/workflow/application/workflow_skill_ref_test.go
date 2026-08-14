package application_test

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

type builtinClassifier struct{}

func (builtinClassifier) IsBuiltinSkill(id string) bool { return strings.HasPrefix(id, "builtin:") }

func skillRefSpec(skillID string) domain.Spec {
	return domain.Spec{Nodes: []domain.Node{
		{ID: "agent", Type: domain.NodeTypeAgent, AgentID: "agent-1"},
		{ID: "skill", Type: domain.NodeTypeSkill, AgentID: "agent-1", SkillID: skillID, SkillRevisionID: "revision-1"},
	}}
}

// TestDefinitionServiceRejectsPlatformManagedSkillRef:保存含 builtin skill 节点的
// spec 时 DefinitionService fail closed(注入 classifier);普通 skill 引用放行。
func TestDefinitionServiceRejectsPlatformManagedSkillRef(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	svc.SetSkillRefClassifier(builtinClassifier{})

	_, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Bad", Spec: skillRefSpec("builtin:platform-guide")}, "u-1")
	require.ErrorIs(t, err, domain.ErrPlatformManagedSkill)

	// 普通 skill 引用 Create/Update 均可保存。
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Good", Spec: skillRefSpec("custom-skill")}, "u-1")
	require.NoError(t, err)
	_, err = svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{
		Name: "Bad2", Spec: skillRefSpec("builtin:platform-guide"), ExpectedRevision: def.Revision,
	}, "u-1")
	require.ErrorIs(t, err, domain.ErrPlatformManagedSkill)
}

// TestDefinitionServiceSkillRefClassifierNotWiredFailClosed:classifier 未注入
// (nil)时含 skill 节点的 spec 保存 fail closed(未知即拒绝),非 skill 节点
// (workflowSpec 仅 agent 节点)不受影响。
func TestDefinitionServiceSkillRefClassifierNotWiredFailClosed(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	_, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "No", Spec: skillRefSpec("custom-skill")}, "u-1")
	require.ErrorIs(t, err, domain.ErrPlatformManagedSkill)
}
