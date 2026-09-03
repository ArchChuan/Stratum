package domain

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// gateTestCase 折叠一个 Decide 输入与期望动作（表驱动）。
type gateTestCase struct {
	name   string
	policy GatePolicy
	ev     GateEvidence
	want   GateAction
}

// confirmReviewVerdict 生成一个已确认回滚的人工结论。
func confirmReviewVerdict() ReviewVerdict { return ReviewVerdictConfirmRollback }

func TestDecideRollbackCandidatesMapToActions(t *testing.T) {
	// 平台 scope 恒 manual：AutoRollbackAllowed=false 已由 policy 折叠（裁决 R4）。
	platform := GatePolicy{Scope: ScopePlatform, RollbackSupported: true, AutoRollbackAllowed: false}
	resourceAuto := GatePolicy{Scope: ScopeResource, RollbackSupported: true, AutoRollbackAllowed: true}
	resourceManual := GatePolicy{Scope: ScopeResource, RollbackSupported: true, AutoRollbackAllowed: false}
	noRollback := GatePolicy{Scope: ScopeResource, RollbackSupported: false, AutoRollbackAllowed: false}

	cases := []gateTestCase{
		{
			name:   "rule1 human confirm rollback -> platform manual",
			policy: platform,
			ev:     GateEvidence{ReviewVerdict: confirmReviewVerdict()},
			want:   GateRollbackManual,
		},
		{
			name:   "rule2 rule blocks >= min -> resource manual",
			policy: resourceManual,
			ev:     GateEvidence{RuleBlockCount: constants.GateRuleBlockRollbackMin},
			want:   GateRollbackManual,
		},
		{
			name:   "rule2 rule blocks >= min -> resource auto",
			policy: resourceAuto,
			ev:     GateEvidence{RuleBlockCount: constants.GateRuleBlockRollbackMin + 1},
			want:   GateRollbackAuto,
		},
		{
			name:   "rollback unsupported -> l2 escalate even when auto allowed absent",
			policy: noRollback,
			ev:     GateEvidence{RuleBlockCount: constants.GateRuleBlockRollbackMin},
			want:   GateL2Escalate,
		},
		{
			name:   "rule3 anomalies >= rollback min and confirmation regressed -> resource auto",
			policy: resourceAuto,
			ev: GateEvidence{
				AnomalyCount:    constants.GateAnomalyRollbackMin,
				ConfirmationRun: &RunComparison{Regressed: true},
			},
			want: GateRollbackAuto,
		},
		{
			name:   "rule3 anomalies high but no confirmation run -> escalate not rollback",
			policy: resourceAuto,
			ev:     GateEvidence{AnomalyCount: constants.GateAnomalyRollbackMin + 2},
			want:   GateL2Escalate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.policy, tc.ev); got != tc.want {
				t.Fatalf("Decide() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDecideRuleOrderingFlagBeforeNone(t *testing.T) {
	// 裁决 R3：rule5（flag/block → l2_escalate）必须先于 rule6（none）。
	// 单条规则阻断（低于回滚门槛 3）仍须 escalate，不能因"低计数"被判 none。
	platform := GatePolicy{Scope: ScopePlatform, RollbackSupported: true, AutoRollbackAllowed: false}
	cases := []gateTestCase{
		{
			name:   "single rule block below rollback min -> escalate",
			policy: platform,
			ev:     GateEvidence{RuleBlockCount: 1},
			want:   GateL2Escalate,
		},
		{
			name:   "single judge flag -> escalate",
			policy: platform,
			ev:     GateEvidence{JudgeFlagCount: 1},
			want:   GateL2Escalate,
		},
		{
			name:   "clean window -> none",
			policy: platform,
			ev:     GateEvidence{},
			want:   GateNone,
		},
		{
			name:   "run regressed without flag/block -> escalate (rule6 none guard)",
			policy: platform,
			ev: GateEvidence{
				AnomalyCount:    1,
				ConfirmationRun: &RunComparison{Regressed: true},
			},
			want: GateL2Escalate,
		},
		{
			name:   "anomalies below alert with regression -> escalate not none",
			policy: platform,
			ev: GateEvidence{
				ConfirmationRun: &RunComparison{Regressed: true},
			},
			want: GateL2Escalate,
		},
		{
			name:   "anomalies at alert floor without flags -> escalate",
			policy: platform,
			ev:     GateEvidence{AnomalyCount: constants.GateAnomalyAlertMin},
			want:   GateL2Escalate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.policy, tc.ev); got != tc.want {
				t.Fatalf("Decide() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMapRollback(t *testing.T) {
	cases := []struct {
		name   string
		policy GatePolicy
		want   GateAction
	}{
		{"unsupported -> escalate", GatePolicy{Scope: ScopeResource, RollbackSupported: false}, GateL2Escalate},
		{"supported + auto -> auto", GatePolicy{Scope: ScopeResource, RollbackSupported: true, AutoRollbackAllowed: true}, GateRollbackAuto},
		{"supported + manual -> manual", GatePolicy{Scope: ScopePlatform, RollbackSupported: true}, GateRollbackManual},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapRollback(tc.policy); got != tc.want {
				t.Fatalf("mapRollback() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunRegressed(t *testing.T) {
	if runRegressed(GateEvidence{}) {
		t.Fatal("runRegressed on empty evidence must be false")
	}
	if runRegressed(GateEvidence{ConfirmationRun: &RunComparison{Regressed: false}}) {
		t.Fatal("runRegressed with Regressed=false must be false")
	}
	if !runRegressed(GateEvidence{ConfirmationRun: &RunComparison{Regressed: true}}) {
		t.Fatal("runRegressed with Regressed=true must be true")
	}
}

func TestGateActionValuesMatchLedgerDecisionText(t *testing.T) {
	// 台账 decision 列直接存 GateAction 文本（eval_gate_actions.decision）。
	for _, a := range []GateAction{GateNone, GateL2Escalate, GateRollbackManual, GateRollbackAuto} {
		if a == "" {
			t.Fatal("GateAction must not be empty string")
		}
	}
}

func TestRunRegressionDeltaThresholdIsNegative(t *testing.T) {
	if constants.RunRegressionDeltaThreshold >= 0 {
		t.Fatal("RunRegressionDeltaThreshold must be negative (dimension delta below baseline)")
	}
}
