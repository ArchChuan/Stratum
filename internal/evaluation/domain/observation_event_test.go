package domain

import (
	"encoding/json"
	"testing"
)

// 契约样例：与 internal/agent/domain/port 的 ObservationEvent JSON 逐字段对齐。
// 若 agent 侧改动事件字段，此 golden 会失败，倒逼同步两侧定义。
const observationEventGolden = `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z"}`

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
}
