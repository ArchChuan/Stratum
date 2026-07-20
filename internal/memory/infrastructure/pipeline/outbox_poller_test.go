package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func TestPollTenantQuarantinesMalformedPayloadBeforeDelete(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, logger: zap.NewNop(), batch: 10}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(7), []byte(`{"broken"`)))
	pool.ExpectExec("INSERT INTO memory_outbox_quarantine").
		WithArgs(int64(7), pgxmock.AnyArg(), "invalid_json").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("DELETE FROM memory_outbox").
		WithArgs([]int64{7}).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()

	if err := poller.pollTenant(context.Background(), "tenant_valid"); err != nil {
		t.Fatal(err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPollTenantKeepsMalformedPayloadWhenQuarantineFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	poller := &OutboxPoller{begin: pool.Begin, logger: zap.NewNop(), batch: 10}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, payload FROM memory_outbox").
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "payload"}).AddRow(int64(8), []byte(`{"broken"`)))
	pool.ExpectExec("INSERT INTO memory_outbox_quarantine").
		WithArgs(int64(8), pgxmock.AnyArg(), "invalid_json").
		WillReturnError(errors.New("quarantine unavailable"))
	pool.ExpectRollback()

	if err := poller.pollTenant(context.Background(), "tenant_valid"); err == nil {
		t.Fatal("expected quarantine failure")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
