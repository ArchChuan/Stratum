package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newAuditMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return mock
}

// batchRecorder stands in for pgx.BatchResults: it answers one CommandTag per
// Exec (or fails at failIdx) so InsertBatch's loop and error path are testable
// without pgxmock batch support.
type batchRecorder struct {
	tags    []pgconn.CommandTag
	failIdx int
	failErr error
	idx     int
}

func (b *batchRecorder) Exec() (pgconn.CommandTag, error) {
	if b.failIdx >= 0 && b.idx == b.failIdx {
		return pgconn.CommandTag{}, b.failErr
	}
	tag := pgconn.CommandTag{}
	if b.idx < len(b.tags) {
		tag = b.tags[b.idx]
	}
	b.idx++
	return tag, nil
}

func (b *batchRecorder) Query() (pgx.Rows, error) { return nil, nil }
func (b *batchRecorder) QueryRow() pgx.Row        { return nil }
func (b *batchRecorder) Close() error             { return nil }

// fakeAuditPool satisfies poolIface: pgxmock for Query/QueryRow/Exec, a
// recorder for SendBatch.
type fakeAuditPool struct {
	pgxmock.PgxPoolIface
	recorder *batchRecorder
}

func (f *fakeAuditPool) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return f.recorder
}

func newBatchAuditMock(t *testing.T, recorder *batchRecorder) *fakeAuditPool {
	t.Helper()
	return &fakeAuditPool{PgxPoolIface: newAuditMock(t), recorder: recorder}
}

func testAuditEvent(id string) domain.AuditEvent {
	return domain.AuditEvent{
		ID: id, TenantID: "t1",
		Actor:  domain.AuditActor{ActorID: "admin-1", ActorType: domain.ActorTypeUser},
		Action: "resource.update", ResourceType: "prompt", ResourceID: "r-1",
		Before:    []byte(`{"name":"v1","password":"secret"}`),
		After:     []byte(`{"name":"v2"}`),
		RequestID: "req-1", TraceID: "trace-1",
		RiskLevel: "high", Outcome: "success", OccurredAt: time.Now(),
	}
}

func TestBuildAuditFilter_allConditions(t *testing.T) {
	from := time.Now().Add(-time.Hour)
	to := time.Now()
	where, args := buildAuditFilter(domain.AuditFilter{
		TenantID: "t1", ActorID: "a1", ResourceType: "prompt", ResourceID: "r1",
		RiskLevel: "high", Action: "update", Outcome: "success", From: from, To: to,
	})
	require.Contains(t, where, "tenant_id = $1")
	require.Contains(t, where, "actor_id = $2")
	require.Contains(t, where, "resource_type = $3")
	require.Contains(t, where, "resource_id = $4")
	require.Contains(t, where, "risk_level = $5")
	require.Contains(t, where, "action = $6")
	require.Contains(t, where, "outcome = $7")
	require.Contains(t, where, "occurred_at >= $8")
	require.Contains(t, where, "occurred_at <= $9")
	require.Len(t, args, 9)
}

func TestBuildAuditFilter_empty(t *testing.T) {
	where, args := buildAuditFilter(domain.AuditFilter{})
	require.Equal(t, "1=1", where)
	require.Empty(t, args)
}

func TestPgAuditRepo_InsertBatch_success(t *testing.T) {
	mock := newBatchAuditMock(t, &batchRecorder{
		failIdx: -1,
		tags:    []pgconn.CommandTag{pgxmock.NewResult("INSERT", 1), pgxmock.NewResult("INSERT", 1)},
	})
	repo := &PgAuditRepo{pool: mock}

	require.NoError(t, repo.InsertBatch(context.Background(),
		[]domain.AuditEvent{testAuditEvent("ev-1"), testAuditEvent("ev-2")}))
	require.Equal(t, 2, mock.recorder.idx)
}

func TestPgAuditRepo_InsertBatch_empty(t *testing.T) {
	repo := &PgAuditRepo{pool: newAuditMock(t)}
	require.NoError(t, repo.InsertBatch(context.Background(), nil))
}

func TestPgAuditRepo_InsertBatch_insertFails(t *testing.T) {
	mock := newBatchAuditMock(t, &batchRecorder{failIdx: 0, failErr: pgx.ErrTxClosed})
	repo := &PgAuditRepo{pool: mock}

	err := repo.InsertBatch(context.Background(), []domain.AuditEvent{testAuditEvent("ev-1")})
	require.ErrorContains(t, err, "audit: batch insert")
	require.Contains(t, err.Error(), "tx is closed")
}

func TestPgAuditRepo_InsertBatch_secondFails(t *testing.T) {
	mock := newBatchAuditMock(t, &batchRecorder{
		tags:    []pgconn.CommandTag{pgxmock.NewResult("INSERT", 1)},
		failIdx: 1, failErr: pgx.ErrTxClosed,
	})
	repo := &PgAuditRepo{pool: mock}

	err := repo.InsertBatch(context.Background(),
		[]domain.AuditEvent{testAuditEvent("ev-1"), testAuditEvent("ev-2")})
	require.ErrorContains(t, err, "audit: batch insert")
}

func TestRedactJSON_empty(t *testing.T) {
	require.Nil(t, redactJSON(nil))
}

func TestRedactJSON_redactsSecrets(t *testing.T) {
	out := redactJSON([]byte(`{"password":"p1","api_key":"k1","token":"t1","name":"v1"}`))
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, "[REDACTED]", got["password"])
	require.Equal(t, "[REDACTED]", got["api_key"])
	require.Equal(t, "[REDACTED]", got["token"])
	require.Equal(t, "v1", got["name"])
}

func TestRedactJSON_keepsPlain(t *testing.T) {
	out := redactJSON([]byte(`{"ok":true,"items":[1,2]}`))
	require.Equal(t, `{"ok":true,"items":[1,2]}`, string(out))
}

func TestPgAuditRepo_Query_filtered(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}
	now := time.Now()

	mock.ExpectQuery("FROM public.audit_events").
		WithArgs("t1", "a1", "prompt", "r1", "high", "update", "success", now.Add(-time.Hour), now, 50, 10).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "tenant_id", "actor_id", "actor_type", "action", "resource_type", "resource_id",
			"before", "after", "request_id", "trace_id", "risk_level", "outcome", "occurred_at",
		}).AddRow("ev-1", "t1", "a1", "user", "update", "prompt", "r1",
			[]byte(`{}`), []byte(`{}`), "req-1", "trace-1", "high", "success", now))

	events, err := repo.Query(context.Background(), domain.AuditFilter{
		TenantID: "t1", ActorID: "a1", ResourceType: "prompt", ResourceID: "r1",
		RiskLevel: "high", Action: "update", Outcome: "success",
		From: now.Add(-time.Hour), To: now, Limit: 50, Offset: 10,
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "ev-1", events[0].ID)
	require.Equal(t, domain.ActorTypeUser, events[0].Actor.ActorType)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgAuditRepo_Query_clampsLimit(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	mock.ExpectQuery("FROM public.audit_events").
		WithArgs("t1", 50, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "tenant_id", "actor_id", "actor_type", "action", "resource_type", "resource_id",
			"before", "after", "request_id", "trace_id", "risk_level", "outcome", "occurred_at",
		}))
	mock.ExpectQuery("FROM public.audit_events").
		WithArgs("t1", 50, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "tenant_id", "actor_id", "actor_type", "action", "resource_type", "resource_id",
			"before", "after", "request_id", "trace_id", "risk_level", "outcome", "occurred_at",
		}))

	// Limit 0 and Limit > 100 both clamp to 50.
	_, err := repo.Query(context.Background(), domain.AuditFilter{TenantID: "t1", Limit: 0})
	require.NoError(t, err)
	_, err = repo.Query(context.Background(), domain.AuditFilter{TenantID: "t1", Limit: 500})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgAuditRepo_Query_queryFails(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	mock.ExpectQuery("FROM public.audit_events").
		WithArgs(50, 0).
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.Query(context.Background(), domain.AuditFilter{Limit: 50})
	require.ErrorContains(t, err, "audit: query")
}

func TestPgAuditRepo_Query_scanFails(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	// json.RawMessage never fails to scan []byte; trigger with a wrong type instead.
	mock.ExpectQuery("FROM public.audit_events").
		WithArgs(50, 0).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "tenant_id", "actor_id", "actor_type", "action", "resource_type", "resource_id",
			"before", "after", "request_id", "trace_id", "risk_level", "outcome", "occurred_at",
		}).AddRow("ev-1", "t1", "a1", "user", 42, "prompt", "r1",
			nil, nil, "req-1", "trace-1", "high", "success", time.Now()))

	_, err := repo.Query(context.Background(), domain.AuditFilter{Limit: 50})
	require.ErrorContains(t, err, "audit: scan")
}

func TestPgAuditRepo_GetByID_found(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}
	now := time.Now()

	mock.ExpectQuery("FROM public.audit_events WHERE id").
		WithArgs("ev-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "tenant_id", "actor_id", "actor_type", "action", "resource_type", "resource_id",
			"before", "after", "request_id", "trace_id", "risk_level", "outcome", "occurred_at",
		}).AddRow("ev-1", "t1", "a1", "service", "update", "prompt", "r1",
			[]byte(`{}`), []byte(`{}`), "req-1", "trace-1", "high", "success", now))

	e, err := repo.GetByID(context.Background(), "ev-1")
	require.NoError(t, err)
	require.NotNil(t, e)
	require.Equal(t, "ev-1", e.ID)
	require.Equal(t, domain.ActorTypeService, e.Actor.ActorType)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgAuditRepo_GetByID_notFound(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	mock.ExpectQuery("FROM public.audit_events WHERE id").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)

	e, err := repo.GetByID(context.Background(), "missing")
	require.NoError(t, err)
	require.Nil(t, e)
}

func TestPgAuditRepo_GetByID_queryFails(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	mock.ExpectQuery("FROM public.audit_events WHERE id").
		WithArgs("ev-1").
		WillReturnError(pgx.ErrTxClosed)

	_, err := repo.GetByID(context.Background(), "ev-1")
	require.ErrorContains(t, err, "audit: get by id")
}

func TestPgAuditRepo_DeleteOlderThan_success(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}
	before := time.Now().Add(-30 * 24 * time.Hour)

	mock.ExpectExec("DELETE FROM public.audit_events").
		WithArgs(before).
		WillReturnResult(pgxmock.NewResult("DELETE", 7))

	require.NoError(t, repo.DeleteOlderThan(context.Background(), before))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgAuditRepo_DeleteOlderThan_fails(t *testing.T) {
	mock := newAuditMock(t)
	repo := &PgAuditRepo{pool: mock}

	mock.ExpectExec("DELETE FROM public.audit_events").
		WithArgs(time.Time{}).
		WillReturnError(pgx.ErrTxClosed)

	err := repo.DeleteOlderThan(context.Background(), time.Time{})
	require.ErrorContains(t, err, "audit: delete older than")
}
