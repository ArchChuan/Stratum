package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

const lifecycleTestTenant = "42c9b62d-4f66-4bc4-a1b8-eed81cdae7b1"

func TestMemoryRepoAddRejectsEmptyTenant(t *testing.T) {
	repo := NewMemoryRepo(nil)

	err := repo.Add(context.Background(), &domain.MemoryEntry{ID: "entry-1"})
	if err == nil {
		t.Fatal("expected empty tenant to fail")
	}
}

func TestMemoryRepoAddRejectsNilPool(t *testing.T) {
	repo := NewMemoryRepo(nil)

	err := repo.Add(context.Background(), &domain.MemoryEntry{ID: "entry-1", TenantID: lifecycleTestTenant})
	if err == nil {
		t.Fatal("expected nil persistence pool to fail")
	}
}

func TestMemoryRepoAddUsesSharedTenantValidation(t *testing.T) {
	repo := &MemoryRepo{pool: rejectingTenantPool{}}

	err := repo.Add(context.Background(), &domain.MemoryEntry{ID: "entry-1", TenantID: `bad"tenant`})
	if err == nil || !strings.HasPrefix(err.Error(), "postgres: invalid tenant_id") {
		t.Fatalf("expected shared tenant validation error, got %v", err)
	}
}

func TestMemoryRepoReadsRejectNilPool(t *testing.T) {
	repo := NewMemoryRepo(nil)

	if _, err := repo.Get(context.Background(), lifecycleTestTenant, "entry-1"); err == nil || !strings.Contains(err.Error(), "pool is nil") {
		t.Fatalf("Get must fail closed, got %v", err)
	}
	if _, err := repo.Search(context.Background(), lifecycleTestTenant, "user-1", "query", 10); err == nil || !strings.Contains(err.Error(), "pool is nil") {
		t.Fatalf("Search must fail closed, got %v", err)
	}
}

func TestMemoryRepoStatsRejectsEmptyTenant(t *testing.T) {
	repo := &MemoryRepo{pool: rejectingTenantPool{}}

	if _, err := repo.Stats(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "tenant_id is empty") {
		t.Fatalf("Stats must fail closed, got %v", err)
	}
}

type rejectingTenantPool struct{}

func (rejectingTenantPool) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("transaction should not begin")
}

func TestMemoryRepoDeleteAllByUserCleansOwnedLifecycleRowsAtomically(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := &MemoryRepo{pool: pool}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	for _, table := range []string{"memory_outbox", "memory_extraction_queue", "memory_summaries", "memory_active_snapshots", "memory_entries"} {
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
