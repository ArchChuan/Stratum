package persistence

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestObservationRepositorySave(t *testing.T) {
	mock := newMockRepo(t) // 沿用 mock_test.go 的 mock 池基建（execTenantTx 内部处理 BEGIN + SET LOCAL search_path）
	repo := NewPgObservationRepository(mock)

	obs := &domain.EvalObservation{
		ID: "obs-1", TraceID: "trace-1",
		Resource: domain.ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"},
		Signals: domain.ObservationSignals{Judge: []domain.JudgeSignal{
			{Dimension: "faithfulness", Score: 0.9, Confidence: 0.85},
		}},
		CostPerf:  domain.CostPerf{LatencyMS: 1200, Tokens: 3200, CostUSD: 0.012},
		Verdict:   domain.VerdictPass,
		CreatedAt: time.Now().UTC(),
	}

	expectTenantTx(mock) // BEGIN + SET LOCAL search_path（execTenantTx 内部执行）
	mock.ExpectExec(`INSERT INTO eval_observations`).
		WithArgs("obs-1", "trace-1", "agent", "agent-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), "", "pass", obs.CreatedAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	if err := repo.Save(context.Background(), "t1", obs); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations not met: %v", err)
	}
}

func TestObservationRepositoryGet(t *testing.T) {
	mock := newMockRepo(t)
	repo := NewPgObservationRepository(mock)
	expectTenantTx(mock) // BEGIN + SET LOCAL search_path（execTenantTx 内部执行）

	rows := pgxmock.NewRows([]string{"id", "trace_id", "resource_kind", "resource_id",
		"param_version", "signals", "cost_perf", "stratum", "verdict", "created_at"}).
		AddRow("obs-1", "trace-1", "agent", "agent-1",
			`{"platform":{"group_key":"","version_seq":0},"resource":{"ref":"r1","version":"v3"},"source":"resource"}`,
			`{"rule":null,"judge":[{"dimension":"faithfulness","score":0.9,"confidence":0.85}],"behavior":{"retry":false,"escalation":false,"abandonment":false}}`,
			`{"latency_ms":1200,"tokens":3200,"cost_usd":0.012}`,
			"", "pass", time.Now().UTC())

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at FROM eval_observations WHERE id = $1`)).
		WithArgs("obs-1").
		WillReturnRows(rows)
	mock.ExpectCommit()

	got, err := repo.Get(context.Background(), "t1", "obs-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TraceID != "trace-1" || got.Signals.Judge[0].Score != 0.9 {
		t.Fatalf("Get mismatch: %+v", got)
	}
}

func TestObservationRepositoryGetNoRows(t *testing.T) {
	mock := newMockRepo(t)
	repo := NewPgObservationRepository(mock)
	expectTenantTx(mock) // BEGIN + SET LOCAL search_path（execTenantTx 内部执行）

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at FROM eval_observations WHERE id = $1`)).
		WithArgs("missing-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback() // fn 返回错误时 execTenantTx 走 Rollback 而非 Commit

	got, err := repo.Get(context.Background(), "t1", "missing-1")
	if err != nil {
		t.Fatalf("Get no-rows: %v", err)
	}
	if got != nil {
		t.Fatalf("Get no-rows: want nil, got %+v", got)
	}
}

func TestObservationRepositoryQueryByResource(t *testing.T) {
	mock := newMockRepo(t)
	repo := NewPgObservationRepository(mock)
	expectTenantTx(mock) // BEGIN + SET LOCAL search_path（execTenantTx 内部执行）

	rows := pgxmock.NewRows([]string{"id", "trace_id", "resource_kind", "resource_id",
		"param_version", "signals", "cost_perf", "stratum", "verdict", "created_at"}).
		AddRow("obs-1", "trace-1", "agent", "agent-1", `{}`, `{}`, `{}`, "", "pass", time.Now().UTC())

	mock.ExpectQuery(`FROM eval_observations WHERE resource_kind = \$1 AND resource_id = \$2`).
		WithArgs("agent", "agent-1", 20, 0).
		WillReturnRows(rows)
	mock.ExpectCommit()

	list, err := repo.QueryByResource(context.Background(), "t1", "agent", "agent-1", nil, nil, 20, 0)
	if err != nil {
		t.Fatalf("QueryByResource: %v", err)
	}
	if len(list) != 1 || list[0].TraceID != "trace-1" {
		t.Fatalf("QueryByResource mismatch: %+v", list)
	}
}
