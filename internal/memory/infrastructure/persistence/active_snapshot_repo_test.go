package persistence

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v2"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestActiveSnapshotRepoUpsertOverwritesScopedSnapshot(t *testing.T) {
	pool, _ := pgxmock.NewPool()
	defer pool.Close()
	repo := &ActiveSnapshotRepo{pool: pool}
	now := time.Now().UTC()
	s := &domain.ActiveSnapshot{TenantID: lifecycleTestTenant, UserID: "user-1", AgentID: "agent-1", WorkContext: []string{"new task"}, Source: domain.SnapshotSource{Type: "message", Reference: "msg-2"}, ExpiresAt: now.Add(time.Hour), UpdatedAt: now, Status: domain.SnapshotStatusActive}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec(regexp.QuoteMeta("ON CONFLICT (user_id, agent_id) DO UPDATE SET")).
		WithArgs(s.UserID, s.AgentID, s.WorkContext, s.PersonalContext, s.TopOfMind, []byte(`{"type":"message","reference":"msg-2"}`), s.ExpiresAt, s.UpdatedAt, s.Status).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	if err := repo.Upsert(context.Background(), s); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActiveSnapshotRepoGetIgnoresExpiredAndIsScopeBound(t *testing.T) {
	pool, _ := pgxmock.NewPool()
	defer pool.Close()
	repo := &ActiveSnapshotRepo{pool: pool}

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("WHERE user_id = \\$1 AND agent_id = \\$2.*expires_at > NOW\\(\\)").
		WithArgs("user-1", "agent-1").WillReturnRows(pgxmock.NewRows([]string{"work_context", "personal_context", "top_of_mind", "source", "expires_at", "updated_at", "version", "status"}))
	pool.ExpectCommit()

	got, err := repo.Get(context.Background(), lifecycleTestTenant, "user-1", "agent-1")
	if err != nil || got != nil {
		t.Fatalf("expired/missing snapshot should be ignored, got=%v err=%v", got, err)
	}
}

func TestActiveSnapshotRepoDeleteUsesTenantAndScope(t *testing.T) {
	pool, _ := pgxmock.NewPool()
	defer pool.Close()
	repo := &ActiveSnapshotRepo{pool: pool}
	secondTenant := "52c9b62d-4f66-4bc4-a1b8-eed81cdae7b2"
	for _, tenant := range []string{lifecycleTestTenant, secondTenant} {
		pool.ExpectBegin()
		pool.ExpectExec(regexp.QuoteMeta(`SET LOCAL search_path = "tenant_` + tenant + `", public`)).WillReturnResult(pgxmock.NewResult("SET", 0))
		pool.ExpectExec("DELETE FROM memory_active_snapshots WHERE user_id = \\$1 AND agent_id = \\$2").WithArgs("user-1", "agent-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		pool.ExpectCommit()
		if err := repo.Delete(context.Background(), tenant, "user-1", "agent-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
