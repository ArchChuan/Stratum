package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/pashagolub/pgxmock/v2"
)

func TestCheckpointStore_Upsert(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expiresAt := time.Now().Add(time.Hour)
	expectTenantTx(pool)
	pool.ExpectExec("INSERT INTO agent_execution_checkpoints").WithArgs(
		"exec-1", "trace-1", "conv-1", "agent-1", "user-1", "tool", 2,
		`[{"role":"user"}]`, `[]`, `[{"id":"call-1"}]`, `{"node":"tool"}`,
		"running", "retry_after_restart", expiresAt,
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	err = store.Upsert(context.Background(), "t1", domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-1", TraceID: "trace-1", ConversationID: "conv-1", AgentID: "agent-1",
		UserID: "user-1", CurrentNode: "tool", StepIndex: 2,
		MessagesSnapshotJSON:   json.RawMessage(`[{"role":"user"}]`),
		CompletedToolCallsJSON: json.RawMessage(`[{"id":"call-1"}]`),
		RuntimeStateJSON:       json.RawMessage(`{"node":"tool"}`), Status: "running",
		ResumeReason: "retry_after_restart", ExpiresAt: expiresAt,
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
	pool.ExpectQuery("SELECT id, execution_id").WithArgs("exec-1").WillReturnRows(pgxmock.NewRows([]string{
		"id", "execution_id", "trace_id", "conversation_id", "agent_id", "user_id",
		"current_node", "step_index", "messages_snapshot_json", "pending_tool_calls_json",
		"completed_tool_calls_json", "runtime_state_json", "status", "resume_reason",
		"created_at", "updated_at", "expires_at",
	}).AddRow(
		"checkpoint-1", "exec-1", "trace-1", "conv-1", "agent-1", "user-1", "tool", 2,
		[]byte(`[{"role":"user"}]`), []byte(`[]`), []byte(`[{"id":"call-1"}]`),
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
	pool.ExpectExec("UPDATE agent_execution_checkpoints").WithArgs("exec-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()
	if err := store.MarkCompleted(context.Background(), "t1", "exec-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
