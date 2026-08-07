package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefinitionServiceUpdate(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)

	// 正常更新：revision 匹配 → 字段生效、Revision 递增。
	updated, err := svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{
		Name: "Renamed", Description: "new desc", ExpectedRevision: def.Revision,
	})
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Name)
	require.Equal(t, def.Revision+1, updated.Revision)

	// 极端情况：revision 冲突 → ErrRevisionConflict，不改动。
	_, err = svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{Name: "X", ExpectedRevision: 0})
	require.ErrorIs(t, err, domain.ErrRevisionConflict)

	// 极端情况：空 name → ErrInvalidSpec。
	_, err = svc.Update(context.Background(), "t1", def.ID, application.UpdateDefinitionCommand{Name: "", ExpectedRevision: updated.Revision})
	require.ErrorIs(t, err, domain.ErrInvalidSpec)

	// 极端情况：ghost id → GetDefinition 错误传播。
	_, err = svc.Update(context.Background(), "t1", "ghost", application.UpdateDefinitionCommand{Name: "X", ExpectedRevision: 0})
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestDefinitionServiceValidate(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)

	// 有效 spec 通过校验。
	require.NoError(t, svc.Validate(context.Background(), "t1", def.ID))

	// 极端情况：空 nodes → GraphValidationError。
	bad := &domain.Definition{ID: "bad", Name: "bad", Revision: 1}
	store.definitions["bad"] = bad
	var gerr *domain.GraphValidationError
	err = svc.Validate(context.Background(), "t1", "bad")
	require.Error(t, err)
	require.ErrorAs(t, err, &gerr)

	// 极端情况：ghost id → 错误传播。
	require.ErrorIs(t, svc.Validate(context.Background(), "t1", "ghost"), domain.ErrNotFound)
}

func TestDefinitionServiceGetAndGetVersion(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	def, err := svc.Create(context.Background(), "t1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)

	// Get 命中；Get ghost → ErrNotFound。
	got, err := svc.Get(context.Background(), "t1", def.ID)
	require.NoError(t, err)
	require.Equal(t, def.ID, got.ID)
	_, err = svc.Get(context.Background(), "t1", "ghost")
	require.ErrorIs(t, err, domain.ErrNotFound)

	// GetVersion：发布后命中；ghost → ErrNotFound。
	version, err := svc.Publish(context.Background(), "t1", def.ID)
	require.NoError(t, err)
	v, err := svc.GetVersion(context.Background(), "t1", version.ID)
	require.NoError(t, err)
	require.Equal(t, version.ID, v.ID)
	_, err = svc.GetVersion(context.Background(), "t1", "ghost")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

// eventRecordingStore 记录 ListEvents 参数并可脚本化事件/错误。
type eventRecordingStore struct {
	*memoryStore
	events []domain.Event
	err    error
	after  int64
	limit  int
}

func (s *eventRecordingStore) AppendEvent(_ context.Context, _ string, event domain.Event) (domain.Event, error) {
	return event, nil
}

func (s *eventRecordingStore) ListEvents(_ context.Context, _, _ string, after int64, limit int) ([]domain.Event, error) {
	s.after, s.limit = after, limit
	return s.events, s.err
}

func seededRunStore(run *domain.Run) *eventRecordingStore {
	store := &eventRecordingStore{memoryStore: newMemoryStore()}
	store.runs[run.ID] = run
	return store
}

func TestRunServiceEvents(t *testing.T) {
	store := seededRunStore(&domain.Run{ID: "run-1", Status: domain.RunStatusRunning, CreatedBy: "operator"})
	store.events = []domain.Event{{ID: "e1"}, {ID: "e2"}}
	runs := application.NewRunServiceWithRegistry(store, store, nil, (&ids{}).NewID, zap.NewNop())

	// 正常：admin 读取，after/limit 透传。
	events, err := runs.Events(context.Background(), "t1", "run-1", adminActor(), 7, 5)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, int64(7), store.after)
	require.Equal(t, 5, store.limit)

	// 极端情况：limit<=0 → 默认 100。
	_, err = runs.Events(context.Background(), "t1", "run-1", adminActor(), 0, 0)
	require.NoError(t, err)
	require.Equal(t, 100, store.limit)

	// 极端情况：limit 超上限 → 收敛 100。
	_, err = runs.Events(context.Background(), "t1", "run-1", adminActor(), 0, 5000)
	require.NoError(t, err)
	require.Equal(t, 100, store.limit)

	// 极端情况：本人 member 可读事件。
	_, err = runs.Events(context.Background(), "t1", "run-1", application.Actor{UserID: "operator", Role: "member"}, 0, 0)
	require.NoError(t, err)

	// 极端情况：非本人 member → ErrNotFound。
	_, err = runs.Events(context.Background(), "t1", "run-1", application.Actor{UserID: "other", Role: "member"}, 0, 0)
	require.ErrorIs(t, err, domain.ErrNotFound)

	// 极端情况：ghost run → ErrNotFound。
	_, err = runs.Events(context.Background(), "t1", "ghost", adminActor(), 0, 0)
	require.ErrorIs(t, err, domain.ErrNotFound)

	// 极端情况：ListEvents 失败 → 错误传播。
	store.err = errors.New("db down")
	_, err = runs.Events(context.Background(), "t1", "run-1", adminActor(), 0, 0)
	require.Error(t, err)
}

// controlErrStore 让 GetRun 可脚本化失败（controlStore 无法表达 ghost/错误）。
type controlErrStore struct {
	controlStore
	getErr error
}

func (s *controlErrStore) GetRun(context.Context, string, string) (*domain.Run, error) {
	return nil, s.getErr
}

func TestControlServiceAvailableActions(t *testing.T) {
	cases := []struct {
		name      string
		run       *domain.Run
		approvals []domain.Approval
		effects   []domain.EffectIntent
		actor     application.Actor
		expected  []string
	}{
		{name: "running", run: &domain.Run{ID: "run-1", Status: domain.RunStatusRunning, CreatedBy: "operator"}, actor: adminActor(), expected: []string{"pause", "cancel"}},
		{name: "queued", run: &domain.Run{ID: "run-1", Status: domain.RunStatusQueued, CreatedBy: "operator"}, actor: adminActor(), expected: []string{"pause", "cancel"}},
		{name: "cancel requested", run: &domain.Run{ID: "run-1", Status: domain.RunStatusCancelRequested, CreatedBy: "operator"}, actor: adminActor(), expected: []string{"cancel"}},
		{name: "pause requested", run: &domain.Run{ID: "run-1", Status: domain.RunStatusPauseRequested, CreatedBy: "operator"}, actor: adminActor(), expected: []string{"cancel"}},
		{name: "paused no approval", run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, CreatedBy: "operator"}, actor: adminActor(), expected: []string{"resume", "cancel"}},
		{name: "paused pending approval", run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, CreatedBy: "operator"}, approvals: []domain.Approval{{ID: "a1", Status: domain.ApprovalStatusPending}}, actor: adminActor(), expected: []string{"cancel"}},
		{name: "manual intervention", run: &domain.Run{ID: "run-1", Status: domain.RunStatusManualIntervention, CreatedBy: "operator"}, effects: []domain.EffectIntent{{ID: "e1", EffectClass: domain.EffectClassNonIdempotent, Status: domain.EffectIntentStatusUnknown}}, actor: adminActor(), expected: []string{"mark_succeeded", "retry", "terminate"}},
		{name: "manual intervention no effect", run: &domain.Run{ID: "run-1", Status: domain.RunStatusManualIntervention, CreatedBy: "operator"}, actor: adminActor(), expected: nil},
		{name: "terminal", run: &domain.Run{ID: "run-1", Status: domain.RunStatusCompleted, CreatedBy: "operator"}, actor: adminActor(), expected: nil},
		// 极端情况：非 admin 只见 cancel；无 cancel 可选则返回 nil。
		{name: "member sees cancel only", run: &domain.Run{ID: "run-1", Status: domain.RunStatusRunning, CreatedBy: "operator"}, actor: application.Actor{UserID: "operator", Role: "member"}, expected: []string{"cancel"}},
		{name: "member manual no cancel", run: &domain.Run{ID: "run-1", Status: domain.RunStatusManualIntervention, CreatedBy: "operator"}, effects: []domain.EffectIntent{{ID: "e1", EffectClass: domain.EffectClassNonIdempotent, Status: domain.EffectIntentStatusUnknown}}, actor: application.Actor{UserID: "operator", Role: "member"}, expected: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &controlStore{run: tc.run, approvals: tc.approvals, effects: tc.effects}
			svc := application.NewControlService(store, func() string { return "event-1" })
			actions, err := svc.AvailableActions(context.Background(), "t1", "run-1", tc.actor)
			require.NoError(t, err)
			require.Equal(t, tc.expected, actions)
		})
	}

	// 极端情况：GetRun 失败 → 错误传播。
	svc := application.NewControlService(&controlErrStore{getErr: errors.New("db down")}, func() string { return "event" })
	_, err := svc.AvailableActions(context.Background(), "t1", "run-1", adminActor())
	require.Error(t, err)
}

func TestControlServiceListApprovalsAndEffects(t *testing.T) {
	approval := domain.NewApproval("approval-1", "run-1", "node", "attempt", 1, "risk", "high", "safe")
	effect := domain.NewEffectIntent("effect-1", "run-1", "node", "attempt", 1, domain.EffectClassIdempotent, "key")
	store := &controlStore{run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, CreatedBy: "operator"}, approvals: []domain.Approval{*approval}, effects: []domain.EffectIntent{*effect}}
	svc := application.NewControlService(store, func() string { return "event-1" })

	// 正常：pending 透传 + 授权通过。
	approvals, err := svc.ListApprovals(context.Background(), "t1", "run-1", adminActor(), true)
	require.NoError(t, err)
	require.Len(t, approvals, 1)
	effects, err := svc.ListEffects(context.Background(), "t1", "run-1", adminActor())
	require.NoError(t, err)
	require.Len(t, effects, 1)

	// 极端情况：本人 member 可读。
	_, err = svc.ListApprovals(context.Background(), "t1", "run-1", application.Actor{UserID: "operator", Role: "member"}, false)
	require.NoError(t, err)
	_, err = svc.ListEffects(context.Background(), "t1", "run-1", application.Actor{UserID: "operator", Role: "member"})
	require.NoError(t, err)

	// 极端情况：非本人 member → ErrNotFound。
	_, err = svc.ListApprovals(context.Background(), "t1", "run-1", application.Actor{UserID: "other", Role: "member"}, false)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = svc.ListEffects(context.Background(), "t1", "run-1", application.Actor{UserID: "other", Role: "member"})
	require.ErrorIs(t, err, domain.ErrNotFound)

	// 极端情况：ghost run / 仓库错误 → 传播。
	bad := application.NewControlService(&controlErrStore{getErr: domain.ErrNotFound}, func() string { return "event" })
	_, err = bad.ListApprovals(context.Background(), "t1", "ghost", adminActor(), false)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = bad.ListEffects(context.Background(), "t1", "ghost", adminActor())
	require.ErrorIs(t, err, domain.ErrNotFound)
}
