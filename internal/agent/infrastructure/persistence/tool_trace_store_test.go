package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/pashagolub/pgxmock/v2"
)

func TestToolTraceStore_InsertBatch(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgToolTraceStore(pool)
	startedAt := time.Now()
	endedAt := startedAt.Add(12 * time.Millisecond)

	expectTenantTx(pool)
	pool.ExpectExec("INSERT INTO agent_tool_traces").
		WithArgs(
			"trace-1", "", "conv-1", "agent-1", "user-1", 2,
			"call-1", "calc", domain.ToolTypeSkill, domain.ProviderTypeSkill, "skill-calc",
			"", "skill-calc", `{"expr":"6*7"}`, "\"42\"", "42",
			"calc returned: 42", domain.ToolTraceStatusSuccess, "", int64(12), false,
			`{"version_id":"v1"}`, startedAt, endedAt,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	err = store.InsertBatch(context.Background(), "t1", []domain.ToolObservation{{
		TraceID:        "trace-1",
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		UserID:         "user-1",
		StepIndex:      2,
		ToolCallID:     "call-1",
		ToolName:       "calc",
		ToolType:       domain.ToolTypeSkill,
		ProviderType:   domain.ProviderTypeSkill,
		ProviderID:     "skill-calc",
		CapabilityID:   "skill-calc",
		Arguments:      map[string]any{"expr": "6*7"},
		RawResult:      "42",
		RawText:        "42",
		Summary:        "calc returned: 42",
		Status:         domain.ToolTraceStatusSuccess,
		LatencyMs:      12,
		Metadata:       map[string]any{"version_id": "v1"},
		StartedAt:      startedAt,
		EndedAt:        endedAt,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestToolTraceStore_InsertBatchRedactsSensitiveFields(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgToolTraceStore(pool)
	startedAt := time.Now()
	endedAt := startedAt.Add(time.Millisecond)

	expectTenantTx(pool)
	pool.ExpectExec("INSERT INTO agent_tool_traces").
		WithArgs(
			"trace-1", "", "conv-1", "agent-1", "user-1", 1,
			"call-1", "http_tool", domain.ToolTypeSkill, domain.ProviderTypeSkill, "skill-http",
			"", "skill-http", `{"nested":{"authorization":"[REDACTED]"},"token":"[REDACTED]"}`,
			`{"api_key":"[REDACTED]","ok":true}`, "authorization=[REDACTED]",
			"summary", domain.ToolTraceStatusSuccess, "", int64(1), false, "{}", startedAt, endedAt,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	err = store.InsertBatch(context.Background(), "t1", []domain.ToolObservation{{
		TraceID:        "trace-1",
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		UserID:         "user-1",
		StepIndex:      1,
		ToolCallID:     "call-1",
		ToolName:       "http_tool",
		ToolType:       domain.ToolTypeSkill,
		ProviderType:   domain.ProviderTypeSkill,
		ProviderID:     "skill-http",
		CapabilityID:   "skill-http",
		Arguments:      map[string]any{"token": "secret-token", "nested": map[string]any{"authorization": "Bearer abc"}},
		RawResult:      map[string]any{"api_key": "key", "ok": true},
		RawText:        "authorization=Bearer abc",
		Summary:        "summary",
		Status:         domain.ToolTraceStatusSuccess,
		LatencyMs:      1,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestTraceEventStore_ListByTraceID(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgTraceEventStore(pool)
	now := time.Now()

	expectTenantTx(pool)
	pool.ExpectQuery("SELECT id, trace_id").
		WithArgs("trace-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "trace_id", "execution_id", "conversation_id", "agent_id", "user_id",
			"run_type", "observation_type", "event_type", "step_index", "span_name", "parent_event_id",
			"status", "input_json", "output_json", "summary", "error_message", "model",
			"prompt_tokens", "completion_tokens", "total_tokens", "cost_usd", "latency_ms",
			"tool_trace_id", "provider_type", "provider_id", "node_id", "node_type",
			"workflow_id", "workflow_version", "sequence_no", "metadata_json", "otel_trace_id",
			"otel_span_id", "started_at", "ended_at", "created_at",
		}).AddRow(
			"event-1", "trace-1", "", "conv-1", "agent-1", "user-1",
			domain.RunTypeAgent, domain.ObservationTypeLLM, domain.TraceEventLLMResponse, 1, "react.llm", "", domain.ToolTraceStatusSuccess,
			[]byte(`{"model":"qwen"}`), []byte(`{"content":"ok"}`), "ok", "", "qwen",
			10, 5, 15, 0.001, int64(20), "", domain.ProviderTypeInternal, "llm:qwen",
			"node-1", "llm", "", "", int64(7), []byte(`{"phase":"answer"}`), "otel-trace", "otel-span",
			now.Add(-20*time.Millisecond), now, now,
		))
	pool.ExpectCommit()

	events, err := store.ListByTraceID(context.Background(), "t1", "trace-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].EventType != domain.TraceEventLLMResponse {
		t.Fatalf("unexpected event type: %s", events[0].EventType)
	}
	if events[0].RunType != domain.RunTypeAgent || events[0].ObservationType != domain.ObservationTypeLLM {
		t.Fatalf("unexpected generic trace fields: %+v", events[0])
	}
	if events[0].ProviderID != "llm:qwen" || events[0].SequenceNo != 7 {
		t.Fatalf("unexpected provider/sequence fields: %+v", events[0])
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestTraceEventStore_InsertBatchPersistsGenericObservationFields(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgTraceEventStore(pool)
	startedAt := time.Now()
	endedAt := startedAt.Add(30 * time.Millisecond)

	expectTenantTx(pool)
	pool.ExpectExec("INSERT INTO agent_trace_events").
		WithArgs(
			"trace-1", "", "conv-1", "agent-1", "user-1",
			domain.RunTypeAgent, domain.ObservationTypeTool, domain.TraceEventToolFinished,
			3, "react.tool", "", domain.ToolTraceStatusSuccess,
			`{"tool_name":"calc"}`, `{"summary":"ok"}`, "ok", "", "",
			0, 0, 0, float64(0), int64(30), "call-1",
			domain.ProviderTypeMCP, "server-1", "node-tool", "mcp_tool",
			"workflow-1", "v2", int64(12), `{"server_id":"server-1"}`, "otel-trace", "otel-span",
			startedAt, endedAt,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	err = store.InsertBatch(context.Background(), "t1", []domain.AgentTraceEvent{{
		TraceID:         "trace-1",
		ConversationID:  "conv-1",
		AgentID:         "agent-1",
		UserID:          "user-1",
		RunType:         domain.RunTypeAgent,
		ObservationType: domain.ObservationTypeTool,
		EventType:       domain.TraceEventToolFinished,
		StepIndex:       3,
		SpanName:        "react.tool",
		Status:          domain.ToolTraceStatusSuccess,
		Input:           map[string]any{"tool_name": "calc"},
		Output:          map[string]any{"summary": "ok"},
		Summary:         "ok",
		LatencyMs:       30,
		ToolTraceID:     "call-1",
		ProviderType:    domain.ProviderTypeMCP,
		ProviderID:      "server-1",
		NodeID:          "node-tool",
		NodeType:        "mcp_tool",
		WorkflowID:      "workflow-1",
		WorkflowVersion: "v2",
		SequenceNo:      12,
		Metadata:        map[string]any{"server_id": "server-1"},
		OTelTraceID:     "otel-trace",
		OTelSpanID:      "otel-span",
		StartedAt:       startedAt,
		EndedAt:         endedAt,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_Upsert(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expiresAt := time.Now().Add(time.Hour)

	expectTenantTx(pool)
	pool.ExpectExec("INSERT INTO agent_execution_checkpoints").
		WithArgs(
			"exec-1", "trace-1", "conv-1", "agent-1", "user-1", "tool",
			2, `[{"role":"user"}]`, `[]`, `[{"id":"call-1"}]`, `{"node":"tool"}`,
			"running", "retry_after_restart", expiresAt,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	err = store.Upsert(context.Background(), "t1", domain.AgentExecutionCheckpoint{
		ExecutionID:            "exec-1",
		TraceID:                "trace-1",
		ConversationID:         "conv-1",
		AgentID:                "agent-1",
		UserID:                 "user-1",
		CurrentNode:            "tool",
		StepIndex:              2,
		MessagesSnapshotJSON:   json.RawMessage(`[{"role":"user"}]`),
		CompletedToolCallsJSON: json.RawMessage(`[{"id":"call-1"}]`),
		RuntimeStateJSON:       json.RawMessage(`{"node":"tool"}`),
		Status:                 "running",
		ResumeReason:           "retry_after_restart",
		ExpiresAt:              expiresAt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_GetLatest(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	now := time.Now()

	expectTenantTx(pool)
	pool.ExpectQuery("SELECT id, execution_id").
		WithArgs("exec-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "execution_id", "trace_id", "conversation_id", "agent_id", "user_id",
			"current_node", "step_index", "messages_snapshot_json", "pending_tool_calls_json",
			"completed_tool_calls_json", "runtime_state_json", "status", "resume_reason",
			"created_at", "updated_at", "expires_at",
		}).AddRow(
			"checkpoint-1", "exec-1", "trace-1", "conv-1", "agent-1", "user-1",
			"tool", 2, []byte(`[{"role":"user"}]`), []byte(`[]`), []byte(`[{"id":"call-1"}]`),
			[]byte(`{"node":"tool"}`), "running", "retry_after_restart", now, now, now.Add(time.Hour),
		))
	pool.ExpectCommit()

	checkpoint, err := store.GetLatest(context.Background(), "t1", "exec-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checkpoint.TraceID != "trace-1" || checkpoint.CurrentNode != "tool" {
		t.Fatalf("unexpected checkpoint: %+v", checkpoint)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_MarkCompleted(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)

	expectTenantTx(pool)
	pool.ExpectExec("UPDATE agent_execution_checkpoints").
		WithArgs("exec-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()

	if err := store.MarkCompleted(context.Background(), "t1", "exec-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
