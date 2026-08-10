package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/scheduler/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

const repoTestTenant = "tenant-abc"

var repoFixedNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func newRepoMock(t *testing.T) (pgxmock.PgxPoolIface, *PgScheduledTaskRepo) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	return mock, &PgScheduledTaskRepo{pool: mock}
}

func taskFixture() *domain.ScheduledTask {
	return &domain.ScheduledTask{
		ID: "task-1", Name: "nightly", WorkflowID: "wf-1", VersionID: "ver-1",
		InputTemplate: map[string]any{"task": "summarize"}, CronExpr: "0 9 * * *",
		Enabled: true, NextFireAt: repoFixedNow.Add(time.Hour),
		LastRunStatus: domain.LastRunOK, CreatedBy: "user-1",
		CreatedAt: repoFixedNow, UpdatedAt: repoFixedNow,
	}
}

func TestPgRepoInsert(t *testing.T) {
	mock, repo := newRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectExec(`INSERT INTO scheduled_tasks \(id, name, workflow_id, version_id, input_template`).
		WithArgs(
			"task-1", "nightly", "wf-1", "ver-1", pgxmock.AnyArg(), "0 9 * * *",
			true, repoFixedNow.Add(time.Hour), domain.LastRunOK, "user-1",
			repoFixedNow, repoFixedNow,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	err := repo.Insert(context.Background(), repoTestTenant, taskFixture())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepoGetByID(t *testing.T) {
	t.Run("returns task row", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		rows := mock.NewRows([]string{"id", "name", "workflow_id", "version_id", "input_template",
			"cron_expr", "enabled", "next_fire_at", "last_run_at", "last_run_status",
			"last_error_message", "created_by", "created_at", "updated_at"}).
			AddRow("task-1", "nightly", "wf-1", "ver-1", map[string]any{"task": "summarize"},
				"0 9 * * *", true, repoFixedNow.Add(time.Hour), &repoFixedNow, domain.LastRunOK,
				"", "user-1", repoFixedNow, repoFixedNow)
		mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id=\$1`).
			WithArgs("task-1").WillReturnRows(rows)
		mock.ExpectCommit()

		task, err := repo.GetByID(context.Background(), repoTestTenant, "task-1")
		require.NoError(t, err)
		require.Equal(t, "task-1", task.ID)
		require.Equal(t, map[string]any{"task": "summarize"}, task.InputTemplate)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("translates no rows to not found", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectQuery(`SELECT .+ FROM scheduled_tasks WHERE id=\$1`).
			WithArgs("missing").WillReturnError(pgx.ErrNoRows)
		mock.ExpectRollback()

		_, err := repo.GetByID(context.Background(), repoTestTenant, "missing")
		require.ErrorIs(t, err, domain.ErrScheduledTaskNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPgRepoList(t *testing.T) {
	mock, repo := newRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	mock.ExpectQuery(`SELECT count\(\*\) FROM scheduled_tasks`).WillReturnRows(
		mock.NewRows([]string{"count"}).AddRow(1))
	rows := mock.NewRows([]string{"id", "name", "workflow_id", "version_id", "input_template",
		"cron_expr", "enabled", "next_fire_at", "last_run_at", "last_run_status",
		"last_error_message", "created_by", "created_at", "updated_at"}).
		AddRow("task-1", "nightly", "wf-1", "ver-1", map[string]any{"task": "summarize"},
			"0 9 * * *", true, repoFixedNow.Add(time.Hour), nil, domain.LastRunOK,
			"", "user-1", repoFixedNow, repoFixedNow)
	mock.ExpectQuery(`ORDER BY created_at DESC, id DESC LIMIT \$1 OFFSET \$2`).
		WithArgs(20, 0).WillReturnRows(rows)
	mock.ExpectCommit()

	tasks, total, err := repo.List(context.Background(), repoTestTenant, 20, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, tasks, 1)
	require.Nil(t, tasks[0].LastRunAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepoUpdate(t *testing.T) {
	t.Run("updates and re-enables", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET name=\$2, workflow_id=\$3, version_id=\$4`).
			WithArgs("task-1", "renamed", "wf-1", "ver-1", pgxmock.AnyArg(), "0 8 * * *",
				true, repoFixedNow.Add(time.Hour)).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		task := taskFixture()
		task.Name = "renamed"
		task.CronExpr = "0 8 * * *"
		err := repo.Update(context.Background(), repoTestTenant, task)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero rows maps to not found", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET`).
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
				pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectRollback()

		err := repo.Update(context.Background(), repoTestTenant, taskFixture())
		require.ErrorIs(t, err, domain.ErrScheduledTaskNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPgRepoDelete(t *testing.T) {
	t.Run("deletes row", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id=\$1`).
			WithArgs("task-1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit()

		require.NoError(t, repo.Delete(context.Background(), repoTestTenant, "task-1"))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero rows maps to not found", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`DELETE FROM scheduled_tasks WHERE id=\$1`).
			WithArgs("missing").WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectRollback()

		err := repo.Delete(context.Background(), repoTestTenant, "missing")
		require.ErrorIs(t, err, domain.ErrScheduledTaskNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPgRepoSetEnabled(t *testing.T) {
	t.Run("disable passes nil next", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET enabled=\$2, next_fire_at=COALESCE\(\$3, next_fire_at\)`).
			WithArgs("task-1", false, (*time.Time)(nil)).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		require.NoError(t, repo.SetEnabled(context.Background(), repoTestTenant, "task-1", false, nil))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("re-enable carries recomputed next", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		next := repoFixedNow.Add(24 * time.Hour)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET enabled=\$2, next_fire_at=COALESCE\(\$3, next_fire_at\)`).
			WithArgs("task-1", true, &next).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		require.NoError(t, repo.SetEnabled(context.Background(), repoTestTenant, "task-1", true, &next))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPgRepoListDue(t *testing.T) {
	mock, repo := newRepoMock(t)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	rows := mock.NewRows([]string{"id", "name", "workflow_id", "version_id", "input_template",
		"cron_expr", "enabled", "next_fire_at", "last_run_at", "last_run_status",
		"last_error_message", "created_by", "created_at", "updated_at"}).
		AddRow("task-1", "nightly", "wf-1", "ver-1", map[string]any{},
			"0 9 * * *", true, repoFixedNow.Add(-time.Minute), nil, "",
			"", "user-1", repoFixedNow, repoFixedNow)
	mock.ExpectQuery(`WHERE enabled AND next_fire_at <= \$1 ORDER BY next_fire_at LIMIT \$2`).
		WithArgs(repoFixedNow, 100).WillReturnRows(rows)
	mock.ExpectCommit()

	tasks, err := repo.ListDue(context.Background(), repoTestTenant, repoFixedNow, 100)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRepoRecordFire(t *testing.T) {
	t.Run("advanced when row matched", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		oldNext := repoFixedNow.Add(-time.Minute)
		newNext := repoFixedNow.Add(24 * time.Hour)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET next_fire_at=\$3, last_run_at=\$4`).
			WithArgs("task-1", oldNext, newNext, repoFixedNow, domain.LastRunOK, "").
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit()

		advanced, err := repo.RecordFire(context.Background(), repoTestTenant, "task-1",
			repoFixedNow, domain.LastRunOK, "", oldNext, newNext)
		require.NoError(t, err)
		require.True(t, advanced)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("concurrent loser returns false without error", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		oldNext := repoFixedNow.Add(-time.Minute)
		newNext := repoFixedNow.Add(24 * time.Hour)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET next_fire_at=\$3, last_run_at=\$4`).
			WithArgs("task-1", oldNext, newNext, repoFixedNow, domain.LastRunOK, "").
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))
		mock.ExpectCommit()

		advanced, err := repo.RecordFire(context.Background(), repoTestTenant, "task-1",
			repoFixedNow, domain.LastRunOK, "", oldNext, newNext)
		require.NoError(t, err)
		require.False(t, advanced)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("storage error propagates", func(t *testing.T) {
		mock, repo := newRepoMock(t)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
		mock.ExpectExec(`UPDATE scheduled_tasks SET next_fire_at=\$3, last_run_at=\$4`).
			WithArgs("task-1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(),
				pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(errors.New("disk full"))
		mock.ExpectRollback()

		_, err := repo.RecordFire(context.Background(), repoTestTenant, "task-1",
			repoFixedNow, domain.LastRunOK, "", repoFixedNow, repoFixedNow.Add(time.Hour))
		require.ErrorContains(t, err, "scheduler store record fire")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPgRepoFailClosedOnTenantMismatch(t *testing.T) {
	_, repo := newRepoMock(t)
	ctx := pgstore.WithTenant(context.Background(), &pgstore.TenantContext{TenantID: "other-tenant"})

	err := repo.Insert(ctx, repoTestTenant, taskFixture())
	require.ErrorContains(t, err, "tenant context mismatch")
}
