package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/jackc/pgx/v5"
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
		"running", "retry_after_restart", expiresAt, "", 1,
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
		`[]`, `[]`, `[]`, `{}`, "running", "", expiresAt, "", 1,
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
		"user_query", "run_generation",
		"created_at", "updated_at", "expires_at",
	}).AddRow(
		"checkpoint-1", "exec-1", "trace-1", "conv-1", "agent-1", "user-1", "tool", 2,
		[]byte(`[{"role":"user"}]`), []byte(`[]`), []byte(`[{"id":"call-1"}]`),
		[]byte(`{"node":"tool"}`), "running", "retry_after_restart", "query-1", 1,
		now, now, now.Add(time.Hour),
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

func TestCheckpointStore_GetLatest_None(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	// 无 checkpoint 行 → (nil, nil)：workflow 首跑（executionID 非空但尚未有
	// checkpoint）走 maybeResumeApproval 时据此判定"无审批续跑"，而非把首次执行
	// 误判为失败。
	pool.ExpectQuery("SELECT id, execution_id").WithArgs("wf:run-1:node-1").
		WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()
	cp, err := store.GetLatest(context.Background(), "t1", "wf:run-1:node-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cp != nil {
		t.Fatalf("expected nil, got %+v", cp)
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
	// 完成时把 expires_at 续为 CheckpointTerminalTTL,DeleteExpired 在窗口内
	// 不回收,窗口过后才删除。
	pool.ExpectExec("UPDATE agent_execution_checkpoints").WithArgs("exec-1", constants.CheckpointTerminalTTL).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()
	if err := store.MarkCompleted(context.Background(), "t1", "exec-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_UpdateStatus(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	// terminal 转换(completed/failed/expired)必须携带保留窗口参数,DeleteExpired
	// 才能在 7 天后回收;非 terminal(如 paused)同样传 TTL 参数但 CASE 分支不改
	// expires_at,此处按 SQL 参数断言,行为差异由集成测试覆盖真实 Postgres。
	expectTenantTx(pool)
	pool.ExpectExec("UPDATE agent_execution_checkpoints").WithArgs("completed", "exec-1", constants.CheckpointTerminalTTL).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()
	expectTenantTx(pool)
	pool.ExpectExec("UPDATE agent_execution_checkpoints").WithArgs("paused", "exec-2", constants.CheckpointTerminalTTL).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()
	if err := store.UpdateStatus(context.Background(), "t1", "exec-1", "completed"); err != nil {
		t.Fatalf("terminal update: %v", err)
	}
	if err := store.UpdateStatus(context.Background(), "t1", "exec-2", "paused"); err != nil {
		t.Fatalf("non-terminal update: %v", err)
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
		"user_query", "run_generation",
		"created_at", "updated_at", "expires_at",
	}).AddRow(
		"checkpoint-2", "exec-no-conv", "trace-1", "", "agent-1", "user-1", "llm", 1,
		[]byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`{}`), "running", "", "", 1,
		now, now, now.Add(time.Hour),
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

func TestCheckpointStore_GetLatestActiveByConversation(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	now := time.Now()
	expectTenantTx(pool)
	// waiting_approval 行受 expires_at 门控（updated_at 不再推进），user_query
	// 从新列读取——GetActiveExecution 刷新恢复的前置数据。
	pool.ExpectQuery("SELECT id, execution_id").WithArgs("conv-1", constants.ActiveExecutionFreshnessWindow).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "execution_id", "trace_id", "conversation_id", "agent_id", "user_id",
			"current_node", "step_index", "messages_snapshot_json", "pending_tool_calls_json",
			"completed_tool_calls_json", "runtime_state_json", "status", "resume_reason",
			"user_query", "run_generation",
			"created_at", "updated_at", "expires_at",
		}).AddRow(
			"checkpoint-1", "exec-1", "trace-1", "conv-1", "agent-1", "user-1", "tool_approval", 3,
			[]byte(`[]`), []byte(`[{"approval_id":"a-1"}]`), []byte(`[]`),
			[]byte(`{"approval_id":"a-1"}`), "waiting_approval", "destructive_tool_approval",
			"query-1", 1, now, now, now.Add(time.Hour),
		))
	pool.ExpectCommit()
	cp, err := store.GetLatestActiveByConversation(context.Background(), "t1", "conv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cp == nil {
		t.Fatal("expected active checkpoint")
	}
	if cp.Status != "waiting_approval" || cp.UserQuery != "query-1" || cp.RunGeneration != 1 {
		t.Fatalf("unexpected checkpoint: %+v", cp)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_GetLatestActiveByConversation_None(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	// 无活跃行 → (nil, nil)：GetActiveExecution 据此返回 404-none，而非报错。
	pool.ExpectQuery("SELECT id, execution_id").WithArgs("conv-1", constants.ActiveExecutionFreshnessWindow).
		WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()
	cp, err := store.GetLatestActiveByConversation(context.Background(), "t1", "conv-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cp != nil {
		t.Fatalf("expected nil, got %+v", cp)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_UpdateStatusFrom_CASMiss(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	// 抢占 CAS：状态不匹配（0 行）必须报错，调用方据此判定并发续跑已胜出。
	pool.ExpectExec("UPDATE agent_execution_checkpoints").
		WithArgs("running", "exec-1", constants.CheckpointTerminalTTL, "waiting_approval").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectRollback()
	err = store.UpdateStatusFrom(context.Background(), "t1", "exec-1", "waiting_approval", "running")
	if err == nil {
		t.Fatal("expected error on CAS miss")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_AdvanceRunGeneration_Stale(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	// 分代 CAS：expect 不匹配（0 行）= 另一个 tab/设备已抢占续跑。
	pool.ExpectExec("UPDATE agent_execution_checkpoints").
		WithArgs("exec-1", 1).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectRollback()
	err = store.AdvanceRunGeneration(context.Background(), "t1", "exec-1", 1)
	if err == nil {
		t.Fatal("expected error on stale generation")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCheckpointStore_Terminate_NoActiveRow(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgCheckpointStore(pool)
	expectTenantTx(pool)
	// 无 active 行（已终态/不存在）→ 报错，调用方据此避免把终态再标记一次。
	pool.ExpectExec("UPDATE agent_execution_checkpoints").
		WithArgs("failed", constants.CheckpointTerminalTTL, "exec-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectRollback()
	err = store.Terminate(context.Background(), "t1", "exec-1", "failed")
	if err == nil {
		t.Fatal("expected error on missing active row")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
