package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

var fixedTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// expectAppendEvent matches the appendEventTx tail of a run mutation:
// bump next_event_sequence, then insert the event row. Mirrors the store's
// defaulting of an empty actor type to "system" and JSON-marshals the payload
// exactly as the store does (nil map serializes to "null").
func expectAppendEvent(mock pgxmock.PgxPoolIface, runID string, event domain.Event) {
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	payload, _ := json.Marshal(event.Payload)
	mock.ExpectQuery("next_event_sequence=next_event_sequence\\+1").
		WithArgs(runID).
		WillReturnRows(pgxmock.NewRows([]string{"seq"}).AddRow(int64(7)))
	mock.ExpectExec("INSERT INTO workflow_events").
		WithArgs(event.ID, runID, int64(7), event.Type, event.NodeID, event.AttemptNo,
			event.Status, event.ActorType, event.ActorID, event.Summary, string(payload), event.OccurredAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

func TestPgStore_ControlRun_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	event := domain.Event{
		ID: "e1", Type: "run.paused", ActorType: "admin", ActorID: "u1",
		Summary: "user paused", Payload: map[string]any{}, OccurredAt: fixedTime,
	}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1").
		WithArgs(domain.RunStatus("paused"), "user request", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", Type: "run.paused", Status: "paused",
		ActorType: "admin", ActorID: "u1", Summary: "user paused",
		Payload: map[string]any{}, OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.ControlRun(context.Background(), "t1", "r1", 4,
		domain.RunStatusPaused, "user request", event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ControlRun_notFound(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1").
		WithArgs(domain.RunStatus("paused"), "user request", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT generation FROM workflow_runs WHERE id=\\$1").
		WithArgs("r1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.ControlRun(context.Background(), "t1", "r1", 4,
		domain.RunStatusPaused, "user request", domain.Event{})
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ControlRun_generationConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1").
		WithArgs(domain.RunStatus("paused"), "user request", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT generation FROM workflow_runs WHERE id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows([]string{"generation"}).AddRow(int64(9)))
	mock.ExpectRollback()

	err := store.ControlRun(context.Background(), "t1", "r1", 4,
		domain.RunStatusPaused, "user request", domain.Event{})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ControlRun_invalidTransition(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1").
		WithArgs(domain.RunStatus("canceled"), "user request", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectQuery("SELECT generation FROM workflow_runs WHERE id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows([]string{"generation"}).AddRow(int64(4)))
	mock.ExpectRollback()

	err := store.ControlRun(context.Background(), "t1", "r1", 4,
		domain.RunStatusCanceled, "user request", domain.Event{})
	require.ErrorIs(t, err, domain.ErrInvalidTransition)
	require.NoError(t, mock.ExpectationsWereMet())
}

var approvalColumns = []string{
	"id", "run_id", "node_id", "attempt_id", "run_generation", "reason", "risk",
	"request_summary", "status", "decision_actor", "decision_comment", "decided_at",
}

func approvalRow() []any {
	return []any{
		"a1", "r1", "n1", "att-1", int64(5), "high risk", "high", "summary",
		domain.ApprovalStatus("pending"), "", "", nil,
	}
}

func TestPgStore_CreateApproval_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	approval := domain.NewApproval("a1", "r1", "n1", "att-1", 5, "high risk", "high", "summary")
	event := domain.Event{ID: "e1", Type: "approval.requested", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_approvals").
		WithArgs("a1", "r1", "n1", "att-1", int64(5), "high risk", "high", "summary",
			domain.ApprovalStatus("pending")).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE workflow_runs SET status='paused'").
		WithArgs(int64(5), "high risk", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", NodeID: "n1", Type: "approval.requested", Status: "",
		OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.CreateApproval(context.Background(), "t1", approval, event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CreateApproval_generationConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_approvals").
		WithArgs("a1", "r1", "n1", "att-1", int64(5), "high risk", "high", "summary",
			domain.ApprovalStatus("pending")).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE workflow_runs SET status='paused'").
		WithArgs(int64(5), "high risk", "r1", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	// 幂等分支:UPDATE 未命中时检查 run 是否已由同批首个 approval 置为 paused
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs("r1", int64(5)).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := store.CreateApproval(context.Background(), "t1",
		domain.NewApproval("a1", "r1", "n1", "att-1", 5, "high risk", "high", "summary"),
		domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_GetApproval_found(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_approvals WHERE id=\\$1").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows(approvalColumns).AddRow(approvalRow()...))
	mock.ExpectCommit()

	a, err := store.GetApproval(context.Background(), "t1", "a1")
	require.NoError(t, err)
	require.NotNil(t, a)
	require.Equal(t, "a1", a.ID)
	require.Equal(t, int64(5), a.RunGeneration)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_GetApproval_notFound(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_approvals WHERE id=\\$1").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	a, err := store.GetApproval(context.Background(), "t1", "nope")
	require.Nil(t, a)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ListApprovals_pendingOnly(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_approvals WHERE").
		WithArgs("r1", true).
		WillReturnRows(pgxmock.NewRows(approvalColumns).
			AddRow(approvalRow()...).
			AddRow("a2", "r1", "n2", "att-2", int64(5), "", "low", "s2",
				domain.ApprovalStatus("pending"), "", "", nil))
	mock.ExpectCommit()

	approvals, err := store.ListApprovals(context.Background(), "t1", "r1", true)
	require.NoError(t, err)
	require.Len(t, approvals, 2)
	require.Equal(t, "a2", approvals[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_invalidDecision(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	// Invalid decision fails before any SQL runs.
	err := store.DecideApproval(context.Background(), "t1", "a1", 5, "att-1",
		domain.ApprovalDecision("maybe"), "u1", "ok", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrInvalidSpec)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_approve(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	event := domain.Event{ID: "e1", Type: "approval.decided", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectQuery("UPDATE workflow_approvals SET status=\\$1").
		WithArgs(domain.ApprovalStatus("approved"), "u1", "ok", "a1", int64(5), "att-1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id"}).AddRow("r1"))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", Type: "approval.decided", Status: "approved",
		OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.DecideApproval(context.Background(), "t1", "a1", 5, "att-1",
		domain.ApprovalDecisionApprove, "u1", "ok", event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_reject(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	event := domain.Event{ID: "e1", Type: "approval.decided", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectQuery("UPDATE workflow_approvals SET status=\\$1").
		WithArgs(domain.ApprovalStatus("rejected"), "u1", "no", "a1", int64(5), "att-1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id"}).AddRow("r1"))
	mock.ExpectExec("UPDATE workflow_runs SET status='failed'").
		WithArgs("approval rejected: no", "r1", int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", Type: "approval.decided", Status: "failed",
		OccurredAt: fixedTime,
	})
	// run_failed 事件:ID 与 OccurredAt 由生产代码生成,业务字段精确断言
	mock.ExpectQuery("next_event_sequence=next_event_sequence\\+1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows([]string{"seq"}).AddRow(int64(7)))
	mock.ExpectExec("INSERT INTO workflow_events").
		WithArgs(pgxmock.AnyArg(), "r1", int64(7), "workflow.run_failed", "", int(0),
			"failed", "system", "", "approval rejected: no", "null", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.DecideApproval(context.Background(), "t1", "a1", 5, "att-1",
		domain.ApprovalDecisionReject, "u1", "no", event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_notFound(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("UPDATE workflow_approvals SET status=\\$1").
		WithArgs(domain.ApprovalStatus("approved"), "u1", "ok", "a1", int64(5), "att-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT status FROM workflow_approvals WHERE id=\\$1").
		WithArgs("a1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.DecideApproval(context.Background(), "t1", "a1", 5, "att-1",
		domain.ApprovalDecisionApprove, "u1", "ok", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_alreadyDecided(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("UPDATE workflow_approvals SET status=\\$1").
		WithArgs(domain.ApprovalStatus("approved"), "u1", "ok", "a1", int64(5), "att-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT status FROM workflow_approvals WHERE id=\\$1").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).
			AddRow(domain.ApprovalStatus("approved")))
	mock.ExpectRollback()

	err := store.DecideApproval(context.Background(), "t1", "a1", 5, "att-1",
		domain.ApprovalDecisionApprove, "u1", "ok", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrDecisionConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_DecideApproval_generationConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("UPDATE workflow_approvals SET status=\\$1").
		WithArgs(domain.ApprovalStatus("approved"), "u1", "ok", "a1", int64(9), "att-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT status FROM workflow_approvals WHERE id=\\$1").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows([]string{"status"}).
			AddRow(domain.ApprovalStatus("pending")))
	mock.ExpectRollback()

	err := store.DecideApproval(context.Background(), "t1", "a1", 9, "att-1",
		domain.ApprovalDecisionApprove, "u1", "ok", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CreateEffectIntent_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	intent := domain.NewEffectIntent("i1", "r1", "n1", "att-1", 5,
		domain.EffectClassNonIdempotent, "key-1")

	beginTenantTx(mock)
	mock.ExpectQuery("INSERT INTO workflow_effect_intents").
		WithArgs("i1", "r1", "n1", "att-1", int64(5), domain.EffectClass("non_idempotent"),
			"key-1", domain.EffectIntentStatus("prepared"), "", "").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("i1"))
	mock.ExpectCommit()

	require.NoError(t, store.CreateEffectIntent(context.Background(), "t1", intent))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CreateEffectIntent_fenceConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("INSERT INTO workflow_effect_intents").
		WithArgs("i1", "r1", "n1", "att-1", int64(5), domain.EffectClass("non_idempotent"),
			"key-1", domain.EffectIntentStatus("prepared"), "", "").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.CreateEffectIntent(context.Background(), "t1",
		domain.NewEffectIntent("i1", "r1", "n1", "att-1", 5,
			domain.EffectClassNonIdempotent, "key-1"))
	require.ErrorIs(t, err, domain.ErrFenceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_StartExternalEffect_emptyOwner(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	// Empty owner fails closed before any SQL.
	err := store.StartExternalEffect(context.Background(), "t1",
		domain.NewEffectIntent("i1", "r1", "n1", "att-1", 5,
			domain.EffectClassNonIdempotent, "key-1"), "", 5)
	require.ErrorIs(t, err, domain.ErrFenceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_StartExternalEffect_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	intent := domain.NewEffectIntent("i1", "r1", "n1", "att-1", 5,
		domain.EffectClassNonIdempotent, "key-1")

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT TRUE FROM workflow_runs r JOIN workflow_node_attempts a").
		WithArgs("r1", int64(5), "owner-1", "att-1").
		WillReturnRows(pgxmock.NewRows([]string{"ok"}).AddRow(true))
	mock.ExpectQuery("INSERT INTO workflow_effect_intents").
		WithArgs("i1", "r1", "n1", "att-1", int64(5), domain.EffectClass("non_idempotent"), "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("i1"))
	mock.ExpectCommit()

	require.NoError(t, store.StartExternalEffect(context.Background(), "t1", intent, "owner-1", 5))
	require.Equal(t, domain.EffectIntentStatusStarted, intent.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_StartExternalEffect_fenceConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT TRUE FROM workflow_runs r JOIN workflow_node_attempts a").
		WithArgs("r1", int64(5), "owner-1", "att-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.StartExternalEffect(context.Background(), "t1",
		domain.NewEffectIntent("i1", "r1", "n1", "att-1", 5,
			domain.EffectClassNonIdempotent, "key-1"), "owner-1", 5)
	require.ErrorIs(t, err, domain.ErrFenceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_UpdateEffectIntent_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	intent := &domain.EffectIntent{
		ID: "i1", RunGeneration: 5, Status: domain.EffectIntentStatusSucceeded,
		Reason: "", OutputSummary: "done",
	}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_effect_intents SET status=\\$1").
		WithArgs(domain.EffectIntentStatus("succeeded"), "", "done", "i1", int64(5),
			domain.EffectIntentStatus("started")).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	require.NoError(t, store.UpdateEffectIntent(context.Background(), "t1", intent,
		domain.EffectIntentStatusStarted))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_UpdateEffectIntent_fenceConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_effect_intents SET status=\\$1").
		WithArgs(domain.EffectIntentStatus("succeeded"), "", "done", "i1", int64(5),
			domain.EffectIntentStatus("started")).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := store.UpdateEffectIntent(context.Background(), "t1",
		&domain.EffectIntent{ID: "i1", RunGeneration: 5, Status: domain.EffectIntentStatusSucceeded,
			OutputSummary: "done"},
		domain.EffectIntentStatusStarted)
	require.ErrorIs(t, err, domain.ErrFenceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

var effectIntentColumns = []string{
	"id", "run_id", "node_id", "attempt_id", "run_generation", "effect_class",
	"idempotency_key", "status", "reason", "output_summary",
}

func TestPgStore_ListEffectIntents_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_effect_intents WHERE run_id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows(effectIntentColumns).
			AddRow("i1", "r1", "n1", "att-1", int64(5), domain.EffectClass("non_idempotent"),
				"key-1", domain.EffectIntentStatus("unknown"), "x", "").
			AddRow("i2", "r1", "n2", "att-2", int64(5), domain.EffectClass("pure"),
				"key-2", domain.EffectIntentStatus("started"), "", ""))
	mock.ExpectCommit()

	intents, err := store.ListEffectIntents(context.Background(), "t1", "r1")
	require.NoError(t, err)
	require.Len(t, intents, 2)
	require.Equal(t, domain.EffectIntentStatusUnknown, intents[0].Status)
	require.Equal(t, "i2", intents[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ResolveEffect_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	event := domain.Event{ID: "e1", Type: "effect.resolved", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=\\$1 FOR UPDATE").
		WithArgs("i1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id", "attempt_id", "effect_class", "status", "run_generation"}).
			AddRow("r1", "att-1", domain.EffectClass("non_idempotent"),
				domain.EffectIntentStatus("unknown"), int64(5)))
	mock.ExpectExec("UPDATE workflow_effect_intents SET status=\\$1,output_summary=\\$2").
		WithArgs(domain.EffectIntentStatus("prepared"), "out", "i1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE workflow_node_attempts SET status=\\$1,output_summary=\\$2").
		WithArgs(domain.AttemptStatus("retry_wait"), "out", "att-1", int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1,generation=generation\\+1").
		WithArgs(domain.RunStatus("queued"), "r1", int64(6)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", Type: "effect.resolved", Status: "queued", OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.ResolveEffect(context.Background(), "t1", "i1", 6,
		domain.ManualActionRetry, "out", "", event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ResolveEffect_generationConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=\\$1 FOR UPDATE").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	err := store.ResolveEffect(context.Background(), "t1", "nope", 6,
		domain.ManualActionRetry, "out", "", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ResolveEffect_invalidStatus(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=\\$1 FOR UPDATE").
		WithArgs("i1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id", "attempt_id", "effect_class", "status", "run_generation"}).
			AddRow("r1", "att-1", domain.EffectClass("pure"),
				domain.EffectIntentStatus("started"), int64(5)))
	mock.ExpectRollback()

	err := store.ResolveEffect(context.Background(), "t1", "i1", 6,
		domain.ManualActionRetry, "out", "", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrInvalidTransition)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ResolveEffect_invalidAction(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=\\$1 FOR UPDATE").
		WithArgs("i1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id", "attempt_id", "effect_class", "status", "run_generation"}).
			AddRow("r1", "att-1", domain.EffectClass("pure"),
				domain.EffectIntentStatus("unknown"), int64(5)))
	mock.ExpectRollback()

	err := store.ResolveEffect(context.Background(), "t1", "i1", 6,
		domain.ManualAction("explode"), "out", "", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrInvalidTransition)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ResolveEffect_runUpdateStale(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("SELECT run_id::text,attempt_id::text,effect_class,status,run_generation FROM workflow_effect_intents WHERE id=\\$1 FOR UPDATE").
		WithArgs("i1").
		WillReturnRows(pgxmock.NewRows([]string{"run_id", "attempt_id", "effect_class", "status", "run_generation"}).
			AddRow("r1", "att-1", domain.EffectClass("non_idempotent"),
				domain.EffectIntentStatus("unknown"), int64(5)))
	mock.ExpectExec("UPDATE workflow_effect_intents SET status=\\$1,output_summary=\\$2").
		WithArgs(domain.EffectIntentStatus("prepared"), "out", "i1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE workflow_node_attempts SET status=\\$1,output_summary=\\$2").
		WithArgs(domain.AttemptStatus("retry_wait"), "out", "att-1", int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1,generation=generation\\+1").
		WithArgs(domain.RunStatus("queued"), "r1", int64(6)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := store.ResolveEffect(context.Background(), "t1", "i1", 6,
		domain.ManualActionRetry, "out", "", domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

var attemptColumns = []string{
	"id", "run_id", "node_id", "attempt_no", "status", "input_text", "output_summary",
	"error_message", "error_code", "trace_id", "fence_token", "run_generation", "retry_at",
	"effect_class", "selected_edges_json",
}

func attemptRow() []any {
	return []any{
		"att-1", "r1", "n1", 1, domain.AttemptStatus("running"), "in", "out",
		"", "", "trace-1", int64(7), int64(5), nil, domain.EffectClass("pure"),
		[]byte(`["e1"]`),
	}
}

func TestPgStore_SaveAttempt_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	attempt := domain.NodeAttempt{
		ID: "att-1", RunID: "r1", NodeID: "n1", AttemptNo: 1,
		Status: domain.AttemptStatusRunning, Input: "in", OutputSummary: "out",
		TraceID: "trace-1", FenceToken: 7, RunGeneration: 5, EffectClass: domain.EffectClassPure,
		SelectedEdges: []string{"e1"},
	}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_node_attempts").
		WithArgs("att-1", "r1", "n1", 1, domain.AttemptStatus("running"), "in", "out",
			"", "", "trace-1", int64(7), int64(5), (*time.Time)(nil), domain.EffectClass("pure"),
			`["e1"]`).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, store.SaveAttempt(context.Background(), "t1", attempt))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_SaveAttempt_fenceConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	attempt := domain.NodeAttempt{
		ID: "att-1", RunID: "r1", NodeID: "n1", AttemptNo: 1,
		Status: domain.AttemptStatusRunning, TraceID: "trace-1", FenceToken: 7,
		RunGeneration: 5, EffectClass: domain.EffectClassPure,
	}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_node_attempts").
		WithArgs("att-1", "r1", "n1", 1, domain.AttemptStatus("running"), "", "",
			"", "", "trace-1", int64(7), int64(5), (*time.Time)(nil), domain.EffectClass("pure"),
			`null`).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectRollback()

	err := store.SaveAttempt(context.Background(), "t1", attempt)
	require.ErrorIs(t, err, domain.ErrFenceConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_SaveAttempt_execFails(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	attempt := domain.NodeAttempt{ID: "att-1", RunID: "r1", NodeID: "n1", AttemptNo: 1,
		Status: domain.AttemptStatusRunning, FenceToken: 7, RunGeneration: 5}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_node_attempts").
		WithArgs("att-1", "r1", "n1", 1, domain.AttemptStatus("running"), "", "",
			"", "", "", int64(7), int64(5), (*time.Time)(nil), domain.EffectClass(""), `null`).
		WillReturnError(pgx.ErrTxClosed)
	mock.ExpectRollback()

	err := store.SaveAttempt(context.Background(), "t1", attempt)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CheckpointAttempt_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	attempt := domain.NodeAttempt{
		ID: "att-1", RunID: "r1", NodeID: "n1", AttemptNo: 2,
		Status: domain.AttemptStatusSucceeded, OutputSummary: "done",
		FenceToken: 7, RunGeneration: 5,
	}
	event := domain.Event{ID: "e1", Type: "attempt.completed", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectExec("INSERT INTO workflow_node_attempts").
		WithArgs("att-1", "r1", "n1", 2, domain.AttemptStatus("succeeded"), "", "done",
			"", "", "", int64(7), int64(5), (*time.Time)(nil), domain.EffectClass(""), `null`).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", NodeID: "n1", AttemptNo: 2, Type: "attempt.completed",
		Status: "succeeded", OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.CheckpointAttempt(context.Background(), "t1", attempt, event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CheckpointRun_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	run := &domain.Run{ID: "r1", Status: domain.RunStatusCompleted, Output: "out", Generation: 5}
	event := domain.Event{ID: "e1", Type: "run.completed", OccurredAt: fixedTime}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1,output_text=\\$2").
		WithArgs(domain.RunStatus("completed"), "out", "", "", "", "", "r1", int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectAppendEvent(mock, "r1", domain.Event{
		ID: "e1", RunID: "r1", Type: "run.completed", Status: "completed", OccurredAt: fixedTime,
	})
	mock.ExpectCommit()

	require.NoError(t, store.CheckpointRun(context.Background(), "t1", run, event))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_CheckpointRun_generationConflict(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectExec("UPDATE workflow_runs SET status=\\$1,output_text=\\$2").
		WithArgs(domain.RunStatus("completed"), "out", "", "", "", "", "r1", int64(5)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	err := store.CheckpointRun(context.Background(), "t1",
		&domain.Run{ID: "r1", Status: domain.RunStatusCompleted, Output: "out", Generation: 5},
		domain.Event{ID: "e1"})
	require.ErrorIs(t, err, domain.ErrGenerationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ListAttempts_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_node_attempts WHERE run_id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows(attemptColumns).AddRow(attemptRow()...))
	mock.ExpectCommit()

	attempts, err := store.ListAttempts(context.Background(), "t1", "r1")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusRunning, attempts[0].Status)
	require.Equal(t, []string{"e1"}, attempts[0].SelectedEdges)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ListAttempts_unmarshalFails(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	row := attemptRow()
	row[14] = []byte(`{broken`)

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_node_attempts WHERE run_id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows(attemptColumns).AddRow(row...))
	mock.ExpectRollback()

	_, err := store.ListAttempts(context.Background(), "t1", "r1")
	require.ErrorContains(t, err, "invalid character")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_AppendEvent_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}
	event := domain.Event{
		ID: "e1", RunID: "r1", Type: "custom", ActorType: "admin", ActorID: "u1",
		Summary: "s", Payload: map[string]any{}, OccurredAt: fixedTime,
	}

	beginTenantTx(mock)
	expectAppendEvent(mock, "r1", event)
	mock.ExpectCommit()

	returned, err := store.AppendEvent(context.Background(), "t1", event)
	require.NoError(t, err)
	require.Equal(t, int64(7), returned.SequenceNo)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_AppendEvent_emptyID(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	// Missing event id fails closed inside the tenant transaction.
	beginTenantTx(mock)
	mock.ExpectRollback()

	_, err := store.AppendEvent(context.Background(), "t1", domain.Event{RunID: "r1"})
	require.ErrorContains(t, err, "workflow event id is required")
	require.NoError(t, mock.ExpectationsWereMet())
}

var eventColumns = []string{
	"id", "run_id", "sequence_no", "event_type", "status", "node_id", "attempt_no",
	"actor_type", "actor_id", "summary", "payload_json", "created_at",
}

func TestPgStore_ListEvents_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_runs WHERE id=\\$1").
		WithArgs("r1").
		WillReturnRows(pgxmock.NewRows(runColumns).AddRow(runRow("r1")...))
	mock.ExpectCommit()
	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_events WHERE run_id=\\$1 AND sequence_no>\\$2").
		WithArgs("r1", int64(3), 10).
		WillReturnRows(pgxmock.NewRows(eventColumns).AddRow(
			"e1", "r1", int64(4), "run.completed", "completed", "n1", 0,
			"system", "", "s", []byte(`{"k":"v"}`), fixedTime,
		))
	mock.ExpectCommit()

	events, err := store.ListEvents(context.Background(), "t1", "r1", 3, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(4), events[0].SequenceNo)
	require.Equal(t, "v", events[0].Payload["k"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ListEvents_runMissing(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_runs WHERE id=\\$1").
		WithArgs("nope").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := store.ListEvents(context.Background(), "t1", "nope", 0, 10)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ClaimRun_noTenants(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	mock.ExpectQuery("SELECT id::text FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}))

	tenantID, run, claimed, err := store.ClaimRun(context.Background(), "owner", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.Nil(t, run)
	require.Empty(t, tenantID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ClaimRun_success(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	mock.ExpectQuery("SELECT id::text FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	beginTenantTx(mock)
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("WITH candidate AS").
		WithArgs("owner", "1m0s", 8).
		WillReturnRows(pgxmock.NewRows([]string{"r.id"}).AddRow("run-1"))
	mock.ExpectCommit()
	beginTenantTx(mock)
	mock.ExpectQuery("FROM workflow_runs WHERE id=\\$1").
		WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows(runColumns).AddRow(runRow("run-1")...))
	mock.ExpectCommit()

	tenantID, run, claimed, err := store.ClaimRun(context.Background(), "owner", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, "t1", tenantID)
	require.Equal(t, "run-1", run.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ClaimRun_noRunnableSkipsTenant(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	mock.ExpectQuery("SELECT id::text FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	beginTenantTx(mock)
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("WITH candidate AS").
		WithArgs("owner", "1m0s", 8).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	tenantID, run, claimed, err := store.ClaimRun(context.Background(), "owner", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.Nil(t, run)
	require.Empty(t, tenantID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ClaimRun_missingSchemaSkipped(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	mock.ExpectQuery("SELECT id::text FROM public.tenants").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	beginTenantTx(mock)
	mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectQuery("WITH candidate AS").
		WithArgs("owner", "1m0s", 8).
		WillReturnError(&pgconn.PgError{Code: "42P01"})
	mock.ExpectRollback()

	// Tenant whose workflow tables are not yet provisioned is skipped, not fatal.
	_, _, claimed, err := store.ClaimRun(context.Background(), "owner", time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStore_ClaimRun_tenantQueryFails(t *testing.T) {
	mock := newStoreMock(t)
	store := &PgStore{pool: mock}

	mock.ExpectQuery("SELECT id::text FROM public.tenants").
		WillReturnError(pgx.ErrTxClosed)

	_, _, _, err := store.ClaimRun(context.Background(), "owner", time.Minute)
	require.ErrorIs(t, err, pgx.ErrTxClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}
