//go:build integration

package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgCandidateCommandRepositoryReplayAndIsolation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; candidate command integration test requires PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenantID := fmt.Sprintf("candidate_commands_%d", time.Now().UnixNano())
	otherTenantID := tenantID + "_other"
	for _, id := range []string{tenantID, otherTenantID} {
		if err := postgres.ProvisionTenantSchema(ctx, pool, id); err != nil {
			t.Fatal(err)
		}
		seedCandidateCommand(t, ctx, pool, id)
		id := id
		t.Cleanup(func() { _, _ = pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, id)) })
	}
	repo := NewPgCandidateCommandRepository(pool)
	command := domain.CandidateCommand{ActorID: "admin-1", ActorType: domain.ActorTypeAdmin, Reason: "unsafe",
		IdempotencyKey: "request-1", ExpectedStateVersion: 1}

	first, err := repo.Reject(ctx, tenantID, "candidate-1", command)
	if err != nil || first.Status != "rejected" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := repo.Reject(ctx, tenantID, "candidate-1", command)
	if err != nil || replay != first {
		t.Fatalf("replay=%+v first=%+v err=%v", replay, first, err)
	}
	changed := command
	changed.Reason = "different"
	if _, err := repo.Reject(ctx, tenantID, "candidate-1", changed); !errors.Is(err, domain.ErrCandidateCommandConflict) {
		t.Fatalf("same-key changed payload err=%v", err)
	}
	stale := command
	stale.IdempotencyKey = "stale"
	stale.ExpectedStateVersion = 2
	if _, err := repo.Reject(ctx, otherTenantID, "candidate-1", stale); !errors.Is(err, domain.ErrCandidateStateConflict) {
		t.Fatalf("stale err=%v", err)
	}
	if _, err := repo.Reject(ctx, otherTenantID, "tenant-one-only", command); !errors.Is(err, domain.ErrCandidateNotFound) {
		t.Fatalf("tenant isolation err=%v", err)
	}

	commands := []domain.CandidateCommand{command, command}
	commands[0].IdempotencyKey = "concurrent-a"
	commands[1].IdempotencyKey = "concurrent-b"
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, cmd := range commands {
		cmd := cmd
		wg.Add(1)
		go func() { defer wg.Done(); _, err := repo.Reject(ctx, otherTenantID, "candidate-1", cmd); errs <- err }()
	}
	wg.Wait()
	close(errs)
	var success, conflict int
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrCandidateCommandConflict) {
			conflict++
		} else {
			t.Fatalf("concurrent err=%v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent success=%d conflict=%d", success, conflict)
	}
	pool.Close()
	if _, err := repo.Reject(ctx, tenantID, "candidate-1", command); err == nil {
		t.Fatal("closed pool error was swallowed")
	}
}

func seedCandidateCommand(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	schema := `"tenant_` + tenantID + `"`
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.optimization_jobs
		(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status)
		VALUES('job-1','skill','skill-1','revision-1','suite-revision-1','succeeded')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.optimization_candidates
		(id,optimization_job_id,revision_id,parent_revision_id,source)
		VALUES('candidate-1','job-1','revision-2','revision-1','rewrite')`); err != nil {
		t.Fatal(err)
	}
	if tenantID[len(tenantID)-6:] != "_other" {
		if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.optimization_candidates
			(id,optimization_job_id,revision_id,parent_revision_id,source)
			VALUES('tenant-one-only','job-1','revision-3','revision-1','rewrite')`); err != nil {
			t.Fatal(err)
		}
	}
}
