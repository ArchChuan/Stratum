package port

import (
	"encoding/json"
	"testing"
)

func TestObservationEventJSON(t *testing.T) {
	evt := ObservationEvent{
		TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
		AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
		CompletedAt: "2026-08-28T12:00:00.123Z",
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"tenant_id":"t1","trace_id":"trace-1","execution_id":"exec-1","agent_id":"agent-1","resource_kind":"agent","resource_id":"agent-1","completed_at":"2026-08-28T12:00:00.123Z"}`
	if string(raw) != want {
		t.Fatalf("JSON mismatch\n got %s\nwant %s", raw, want)
	}
}
