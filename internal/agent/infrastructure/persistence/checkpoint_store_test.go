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

func TestCheckpointStore_Upsert_EmptyConversationID(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expiresAt := time.Now().Add(time.Hour)
	expectTenantTx(pool)
	// conversation_id 为空字符串时必须写 NULL,否则 UUID 列报
	// `invalid input syntax for type uuid: ""`(checkpoint 全开后无
	// conversation 的执行如 evaluation run 触发,SQLSTATE 22P02)。
	pool.ExpectExec("INSERT INTO agent_execution_checkpoints").WithArgs(
		"exec-no-conv", "trace-1", nil, "agent-1", "user-1", "llm", 1,
		`[]`, `[]`, `[]`, `{}`, "running", "", expiresAt,
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	err = store.Upsert(context.Background(), "t1", domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-no-conv", TraceID: "trace-1", ConversationID: "",
		AgentID: "agent-1", UserID: "user-1", CurrentNode: "llm", StepIndex: 1,
		Status: "running", ExpiresAt: expiresAt,
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

func TestCheckpointStore_DeleteExpired(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	pool.ExpectExec("DELETE FROM agent_execution_checkpoints").
		WillReturnResult(pgxmock.NewResult("DELETE", 3))
	pool.ExpectCommit()
	deleted, err := store.DeleteExpired(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted rows, got %d", deleted)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_GetLatest_NullConversationID(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	now := time.Now()
	expectTenantTx(pool)
	// conversation_id 为 NULL 时,恢复路径必须读到空字符串而非报
	// `cannot scan NULL into *string`(与 Upsert 空→NULL 配套)。
	pool.ExpectQuery("SELECT id, execution_id").WithArgs("exec-no-conv").WillReturnRows(pgxmock.NewRows([]string{
		"id", "execution_id", "trace_id", "conversation_id", "agent_id", "user_id",
		"current_node", "step_index", "messages_snapshot_json", "pending_tool_calls_json",
		"completed_tool_calls_json", "runtime_state_json", "status", "resume_reason",
		"created_at", "updated_at", "expires_at",
	}).AddRow(
		"checkpoint-2", "exec-no-conv", "trace-1", "", "agent-1", "user-1", "llm", 1,
		[]byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`{}`), "running", "", now, now, now.Add(time.Hour),
	))
	pool.ExpectCommit()
	checkpoint, err := store.GetLatest(context.Background(), "t1", "exec-no-conv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checkpoint.ConversationID != "" {
		t.Fatalf("expected empty conversation_id, got %q", checkpoint.ConversationID)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
