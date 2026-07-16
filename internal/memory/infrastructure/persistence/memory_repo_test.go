package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
)

const lifecycleTestTenant = "42c9b62d-4f66-4bc4-a1b8-eed81cdae7b1"

func TestMemoryRepoDeleteAllByUserCleansOwnedLifecycleRowsAtomically(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := &MemoryRepo{pool: pool}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	for _, table := range []string{"memory_outbox", "memory_extraction_queue", "memory_summaries", "memory_entries"} {
		pool.ExpectExec("DELETE FROM " + table + " WHERE user_id = \\$1").
			WithArgs("user-1").
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
	}
	pool.ExpectCommit()

	if err := repo.DeleteAllByUser(context.Background(), lifecycleTestTenant, "user-1"); err != nil {
		t.Fatalf("delete lifecycle rows: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryRepoDeleteAllByAgentRollsBackLifecycleFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := &MemoryRepo{pool: pool}

	wantErr := errors.New("queue unavailable")
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("DELETE FROM memory_outbox WHERE agent_id = \\$1").
		WithArgs("agent-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectExec("DELETE FROM memory_extraction_queue WHERE agent_id = \\$1").
		WithArgs("agent-1").WillReturnError(wantErr)
	pool.ExpectRollback()

	err = repo.DeleteAllByAgent(context.Background(), lifecycleTestTenant, "agent-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped queue error, got %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
