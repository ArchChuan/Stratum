package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// stubGateStore 内存版 GateStore（QueryWindow 预置证据；AppendAction 收台账）。
type stubGateStore struct {
	evidence domain.GateEvidence
	actions  []domain.GateActionRecord
	err      error
}

func (s *stubGateStore) AppendAction(_ context.Context, _ string, rec domain.GateActionRecord) error {
	if s.err != nil {
		return s.err
	}
	s.actions = append(s.actions, rec)
	return nil
}

func (s *stubGateStore) QueryWindow(context.Context, string, domain.GateTarget, time.Time) (domain.GateEvidence, error) {
	return s.evidence, s.err
}

// stubGatePolicy 固定返回策略；err 非 nil 模拟解析失败。
type stubGatePolicy struct {
	policy domain.GatePolicy
	err    error
}

func (s *stubGatePolicy) Resolve(context.Context, domain.GateTarget) (domain.GatePolicy, error) {
	return s.policy, s.err
}

// stubPlatformOps 记录平台 eval_state 写回。
type stubPlatformOps struct {
	states []string
	err    error
}

func (s *stubPlatformOps) UpdateEvalState(_ context.Context, _ string, _ int64, state, _ string) error {
	if s.err != nil {
		return s.err
	}
	s.states = append(s.states, state)
	return nil
}

// gateObs 构造一条平台源观测（组=agent，seq=2）。
func gateObs() domain.EvalObservation {
	return domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"},
		Param: domain.ParamVersion{
			Source:   domain.ParamSourcePlatform,
			Platform: domain.PlatformParamVersion{GroupKey: "agent", VersionSeq: 2},
		},
	}
}

func fixedNow() time.Time { return time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) }

func newTestGate(deps GateServiceDeps, start time.Time) *GateService {
	s := NewGateService(deps)
	s.now = func() time.Time { return start }
	return s
}

func TestHandleObservationRoutesRollbackManualToPlatformWriteback(t *testing.T) {
	ctx := context.Background()
	store := &stubGateStore{evidence: domain.GateEvidence{RuleBlockCount: constants.GateRuleBlockRollbackMin}}
	platform := &stubPlatformOps{}
	svc := newTestGate(GateServiceDeps{
		Cfg:      func(context.Context) domain.GateConfig { return domain.GateConfig{Enabled: true} },
		Repo:     store,
		Policy:   &stubGatePolicy{policy: domain.GatePolicy{Scope: domain.ScopePlatform, RollbackSupported: true}},
		Platform: platform,
	}, fixedNow())

	if err := svc.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatalf("HandleObservation error = %v", err)
	}
	if len(platform.states) != 1 || platform.states[0] != "rollback_recommended" {
		t.Fatalf("platform writeback = %v, want [rollback_recommended]", platform.states)
	}
	if len(store.actions) != 1 {
		t.Fatalf("ledger actions = %d, want 1", len(store.actions))
	}
	rec := store.actions[0]
	if rec.Decision != domain.GateRollbackManual || rec.Action != "rollback_recommended" {
		t.Fatalf("ledger decision/action = %q/%q, want rollback_manual/rollback_recommended", rec.Decision, rec.Action)
	}
	if rec.Target.Scope != domain.ScopePlatform || rec.Target.GroupKey != "agent" || rec.Target.VersionSeq != 2 {
		t.Fatalf("ledger target = %+v, want platform agent seq 2", rec.Target)
	}
}

func TestHandleObservationDisabledOrCleanSkips(t *testing.T) {
	ctx := context.Background()
	disabled := newTestGate(GateServiceDeps{
		Cfg:    func(context.Context) domain.GateConfig { return domain.GateConfig{Enabled: false} },
		Repo:   &stubGateStore{},
		Policy: &stubGatePolicy{},
	}, fixedNow())
	if err := disabled.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatalf("disabled error = %v", err)
	}

	clean := &stubGateStore{} // evidence 全零 → none
	svc := newTestGate(GateServiceDeps{
		Cfg:    func(context.Context) domain.GateConfig { return domain.GateConfig{Enabled: true} },
		Repo:   clean,
		Policy: &stubGatePolicy{policy: domain.GatePolicy{Scope: domain.ScopePlatform, RollbackSupported: true}},
	}, fixedNow())
	if err := svc.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatalf("clean error = %v", err)
	}
	if len(clean.actions) != 0 {
		t.Fatalf("clean window must not append ledger, got %d", len(clean.actions))
	}
}

func TestHandleObservationGateNilRepoDoesNotPanic(t *testing.T) {
	// fail-open：Repo nil（未装配证据源）→ HandleObservation 跳过评估，不 panic。
	ctx := context.Background()
	svc := newTestGate(GateServiceDeps{
		Cfg:    func(context.Context) domain.GateConfig { return domain.GateConfig{Enabled: true} },
		Repo:   nil,
		Policy: &stubGatePolicy{policy: domain.GatePolicy{Scope: domain.ScopeResource, RollbackSupported: true}},
	}, fixedNow())
	if err := svc.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleObservationCooldownSuppressesRapidRepeats(t *testing.T) {
	ctx := context.Background()
	store := &stubGateStore{evidence: domain.GateEvidence{RuleBlockCount: constants.GateRuleBlockRollbackMin}}
	platform := &stubPlatformOps{}
	svc := newTestGate(GateServiceDeps{
		Cfg:      func(context.Context) domain.GateConfig { return domain.GateConfig{Enabled: true} },
		Repo:     store,
		Policy:   &stubGatePolicy{policy: domain.GatePolicy{Scope: domain.ScopePlatform, RollbackSupported: true}},
		Platform: platform,
	}, fixedNow())
	if err := svc.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatal(err)
	}
	// 同 target 仍在冷却期内 → 跳过。
	if err := svc.HandleObservation(ctx, "t1", gateObs()); err != nil {
		t.Fatal(err)
	}
	if len(store.actions) != 1 {
		t.Fatalf("cooldown not applied, ledger actions = %d, want 1", len(store.actions))
	}
}

func TestGateTargetForObservation(t *testing.T) {
	// 纯平台锚点（无资源版本）→ 平台组目标（与 buildObservation 纯平台观测一致：
	// Source unknown、Platform.GroupKey+seq 已填）。
	platform := domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "a1"},
		Param: domain.ParamVersion{Source: domain.ParamSourceUnknown,
			Platform: domain.PlatformParamVersion{GroupKey: "agent", VersionSeq: 3}},
	}
	tgt, ok := domain.GateTargetForObservation(platform)
	if !ok || tgt.Scope != domain.ScopePlatform || tgt.GroupKey != "agent" || tgt.VersionSeq != 3 {
		t.Fatalf("platform mapping = %+v ok=%v", tgt, ok)
	}

	// 资源版本锚点 → 资源目标（Kind+ResourceID+RevisionID=Version）。
	resource := domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "skill", ResourceID: "s1"},
		Param: domain.ParamVersion{Source: domain.ParamSourceResource,
			Resource: domain.ResourceParamVersion{Ref: "rev-9", Version: "rev-9"}},
	}
	tgt, ok = domain.GateTargetForObservation(resource)
	if !ok || tgt.Scope != domain.ScopeResource || tgt.Kind != "skill" || tgt.ResourceID != "s1" || tgt.RevisionID != "rev-9" {
		t.Fatalf("resource mapping = %+v ok=%v", tgt, ok)
	}

	// 双锚点（平台+资源都带版本，Source both）→ 资源优先（回滚被测资源以恢复行为）。
	both := domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "a1"},
		Param: domain.ParamVersion{Source: domain.ParamSourceBoth,
			Platform: domain.PlatformParamVersion{GroupKey: "agent", VersionSeq: 3},
			Resource: domain.ResourceParamVersion{Ref: "rev-9", Version: "rev-9"}},
	}
	tgt, ok = domain.GateTargetForObservation(both)
	if !ok || tgt.Scope != domain.ScopeResource || tgt.ResourceID != "a1" || tgt.RevisionID != "rev-9" {
		t.Fatalf("both mapping = %+v ok=%v", tgt, ok)
	}

	// 平台组带 key 但 seq 0（未发布 unknown）→ 非锚点，不可评估。
	noSeq := domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "a1"},
		Param: domain.ParamVersion{
			Platform: domain.PlatformParamVersion{GroupKey: "agent", VersionSeq: 0}},
	}
	if _, ok := domain.GateTargetForObservation(noSeq); ok {
		t.Fatal("platform anchor without seq must not map to a gate target")
	}

	// 无任何锚点 → 不可评估。
	unversioned := domain.EvalObservation{
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "a1"},
		Param:    domain.ParamVersion{},
	}
	if _, ok := domain.GateTargetForObservation(unversioned); ok {
		t.Fatal("unversioned observation must not map to a gate target")
	}
}

func TestActionLabel(t *testing.T) {
	if got := actionLabel(domain.GateRollbackManual); got != "rollback_recommended" {
		t.Fatalf("manual label = %q", got)
	}
	if got := actionLabel(domain.GateRollbackAuto); got != "rollback_recommended" {
		t.Fatalf("auto label = %q", got)
	}
	if got := actionLabel(domain.GateL2Escalate); got != "escalate" {
		t.Fatalf("escalate label = %q", got)
	}
	if got := actionLabel(domain.GateNone); got != "" {
		t.Fatalf("none label = %q, want empty", got)
	}
}
