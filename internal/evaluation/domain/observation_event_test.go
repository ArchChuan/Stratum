package domain

import (
	"encoding/json"
	"testing"
)

// 契约样例：与 internal/agent/domain/port 的 ObservationEvent JSON 逐字段对齐。
// 若 agent 侧改动事件字段，此 golden 会失败，倒逼同步两侧定义。
const observationEventGolden = `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z"}`

// observationEventGoldenWithSignals 携带 P1b rule/behavior 信号的契约样例（字段
// 顺序与 ObservationReferenceEvent 声明顺序一致：基础字段后接 rule_signals、behavior）。
const observationEventGoldenWithSignals = `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z","rule_signals":[{"rule":"tool_denylist","message":"tool \"x\" blocked by platform rule"}],"behavior":{"retry":true,"escalation":false,"abandonment":true}}`

func TestObservationReferenceEventUnmarshal(t *testing.T) {
	var evt ObservationReferenceEvent
	if err := json.Unmarshal([]byte(observationEventGolden), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if evt.TenantID != "t1" || evt.TraceID != "trace-1" || evt.ExecutionID != "exec-1" {
		t.Fatalf("identity fields mismatch: %+v", evt)
	}
	if evt.AgentID != "agent-1" || evt.ResourceKind != "agent" || evt.ResourceID != "agent-1" {
		t.Fatalf("resource fields mismatch: %+v", evt)
	}
	if evt.CompletedAt != "2026-08-28T12:00:00.123Z" {
		t.Fatalf("completed_at mismatch: %q", evt.CompletedAt)
	}
	// 无信号事件：rule_signals/behavior 缺省 → nil，不报错。
	if evt.RuleSignals != nil || evt.Behavior != nil {
		t.Fatalf("absent signals must stay nil: %+v", evt)
	}
}

func TestObservationReferenceEventUnmarshalSignals(t *testing.T) {
	var evt ObservationReferenceEvent
	if err := json.Unmarshal([]byte(observationEventGoldenWithSignals), &evt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(evt.RuleSignals) != 1 {
		t.Fatalf("rule_signals len = %d, want 1", len(evt.RuleSignals))
	}
	sig := evt.RuleSignals[0]
	if sig.Rule != "tool_denylist" || sig.Message != `tool "x" blocked by platform rule` {
		t.Fatalf("rule signal mismatch: %+v", sig)
	}
	if evt.Behavior == nil {
		t.Fatal("behavior must be non-nil when present")
	}
	if !evt.Behavior.Retry || evt.Behavior.Escalation || !evt.Behavior.Abandonment {
		t.Fatalf("behavior mismatch: %+v", evt.Behavior)
	}
}

func TestObservationReferenceEventMarshalSignalsRoundTrip(t *testing.T) {
	evt := ObservationReferenceEvent{
		TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
		AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
		CompletedAt: "2026-08-28T12:00:00.123Z",
		RuleSignals: []RuleSignalPayload{{Rule: "tool_denylist", Message: `tool "x" blocked by platform rule`}},
		Behavior:    &BehaviorSignalPayload{Retry: true, Abandonment: true},
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != observationEventGoldenWithSignals {
		t.Fatalf("JSON mismatch\n got %s\nwant %s", raw, observationEventGoldenWithSignals)
	}
}

func TestObservationReferenceEventOmitEmptySignals(t *testing.T) {
	evt := ObservationReferenceEvent{
		TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
		AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
		CompletedAt: "2026-08-28T12:00:00.123Z",
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != observationEventGolden {
		t.Fatalf("omitempty violated, empty signals leaked\n got %s\nwant %s", raw, observationEventGolden)
	}
}
