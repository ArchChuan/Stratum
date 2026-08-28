package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/scheduler/domain"
	"github.com/byteBuilderX/stratum/internal/scheduler/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

const testTenant = "tenant-abc"

// fixedNow is a UTC fixed instant; all time-sensitive assertions derive from it.
var fixedNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

type fireStart struct {
	tenantID, versionID string
	input               map[string]any
	key, createdBy      string
}

type fireRecord struct {
	id, status, errorMsg string
	oldNext, newNext     time.Time
}

type fakeRepo struct {
	tasks        map[string]*domain.ScheduledTask
	inserted     []*domain.ScheduledTask
	updated      []*domain.ScheduledTask
	deleted      []string
	enabledCalls []struct {
		id      string
		enabled bool
		next    *time.Time
	}
	dueCalls []struct {
		now   time.Time
		limit int
	}
	records            []fireRecord
	recordFireAdvanced bool
	recordFireErr      error
	getErr             error
	insertErr          error
	updateErr          error
	deleteErr          error
	enableErr          error
	dueErr             error
	listErr            error
	listTotal          int
	listLimit          int
	listOffset         int
}

func (f *fakeRepo) Insert(_ context.Context, _ string, task *domain.ScheduledTask) error {
	f.inserted = append(f.inserted, task)
	return f.insertErr
}
func (f *fakeRepo) GetByID(_ context.Context, _, id string) (*domain.ScheduledTask, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	t, ok := f.tasks[id]
	if !ok {
		return nil, domain.ErrScheduledTaskNotFound
	}
	return t, nil
}
func (f *fakeRepo) List(_ context.Context, _ string, limit, offset int) ([]domain.ScheduledTask, int, error) {
	f.listLimit, f.listOffset = limit, offset
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	var out []domain.ScheduledTask
	for _, t := range f.tasks {
		out = append(out, *t)
	}
	return out, f.listTotal, nil
}
func (f *fakeRepo) Update(_ context.Context, _ string, task *domain.ScheduledTask) error {
	f.updated = append(f.updated, task)
	return f.updateErr
}
func (f *fakeRepo) Delete(_ context.Context, _, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}
func (f *fakeRepo) SetEnabled(_ context.Context, _, id string, enabled bool, next *time.Time) error {
	f.enabledCalls = append(f.enabledCalls, struct {
		id      string
		enabled bool
		next    *time.Time
	}{id, enabled, next})
	return f.enableErr
}
func (f *fakeRepo) ListDue(_ context.Context, _ string, now time.Time, limit int) ([]domain.ScheduledTask, error) {
	f.dueCalls = append(f.dueCalls, struct {
		now   time.Time
		limit int
	}{now, limit})
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	var out []domain.ScheduledTask
	for _, t := range f.tasks {
		if t.Enabled && !t.NextFireAt.After(now) {
			out = append(out, *t)
		}
	}
	return out, nil
}
func (f *fakeRepo) RecordFire(_ context.Context, _, id string, firedAt time.Time, status, errorMsg string, oldNext, newNext time.Time) (bool, error) {
	f.records = append(f.records, fireRecord{id: id, status: status, errorMsg: errorMsg, oldNext: oldNext, newNext: newNext})
	return f.recordFireAdvanced, f.recordFireErr
}

type fakeRunner struct {
	starts []fireStart
	err    error
}

func (f *fakeRunner) StartAsync(_ context.Context, tenantID, versionID string, input map[string]any, key, createdBy string) error {
	f.starts = append(f.starts, fireStart{tenantID: tenantID, versionID: versionID, input: input, key: key, createdBy: createdBy})
	return f.err
}

type fakeResolver struct {
	versionErr   error
	validateErr  error
	definitionID string
	names        map[string]port.VersionName
	namesErr     error
}

func (f *fakeResolver) GetVersion(_ context.Context, _, _ string) (*port.VersionInfo, error) {
	if f.versionErr != nil {
		return nil, f.versionErr
	}
	return &port.VersionInfo{DefinitionID: f.definitionID}, nil
}
func (f *fakeResolver) ValidateInput(_ context.Context, _, _ string, _ map[string]any) error {
	return f.validateErr
}
func (f *fakeResolver) ResolveVersionNames(_ context.Context, _ string, versionIDs []string) (map[string]port.VersionName, error) {
	if f.namesErr != nil {
		return nil, f.namesErr
	}
	out := make(map[string]port.VersionName, len(versionIDs))
	for _, id := range versionIDs {
		if name, ok := f.names[id]; ok {
			out[id] = name
		}
	}
	return out, nil
}

func newTestService(repo *fakeRepo, runner *fakeRunner, resolver *fakeResolver, nowFn func() time.Time) *Service {
	return NewService(repo, runner, resolver, observability.NoopMetrics{}, zap.NewNop(), func() string { return "task-1" }, nowFn)
}

func adminActor() Actor { return Actor{UserID: "user-1", Role: "admin"} }

func dueTask(id string, next time.Time) *domain.ScheduledTask {
	return &domain.ScheduledTask{
		ID: id, Name: "nightly", WorkflowID: "wf-1", VersionID: "ver-1",
		InputTemplate: map[string]any{"task": "summarize"}, CronExpr: "0 9 * * *",
		Enabled: true, NextFireAt: next, CreatedBy: "user-1",
		CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}
}

func TestServiceCreateValidatesCronAndInput(t *testing.T) {
	repo := &fakeRepo{}
	runner := &fakeRunner{}
	resolver := &fakeResolver{definitionID: "wf-1"}
	svc := newTestService(repo, runner, resolver, func() time.Time { return fixedNow })

	t.Run("rejects non-admin actor", func(t *testing.T) {
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, Actor{UserID: "user-1", Role: "member"})
		require.ErrorIs(t, err, domain.ErrScheduledTaskForbidden)
	})

	t.Run("rejects empty user id", func(t *testing.T) {
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, Actor{UserID: "", Role: "admin"})
		require.ErrorIs(t, err, domain.ErrScheduledTaskForbidden)
	})

	t.Run("rejects name over 64 runes", func(t *testing.T) {
		long := strings.Repeat("a", constants.MaxScheduledTaskNameRunes+1)
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: long, WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskInvalidInput)
	})

	t.Run("rejects cron firing more often than once per minute", func(t *testing.T) {
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "@every 100ms",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskInvalidCron)
	})

	t.Run("rejects malformed cron", func(t *testing.T) {
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "not a cron",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskInvalidCron)
	})

	t.Run("propagates version resolution failure", func(t *testing.T) {
		resolver.versionErr = domain.ErrScheduledTaskNotFound
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "missing",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskNotFound)
		resolver.versionErr = nil
	})

	t.Run("rejects version not belonging to the workflow", func(t *testing.T) {
		resolver.definitionID = "wf-other"
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskInvalidInput)
		resolver.definitionID = "wf-1"
	})

	t.Run("rejects input template failing the version schema", func(t *testing.T) {
		resolver.validateErr = errors.New("task is required")
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{"task": ""}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskInvalidInput)
		resolver.validateErr = nil
	})

	t.Run("schedules next fire at UTC and stamps creator", func(t *testing.T) {
		task, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "weekly report", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{"task": "report"}, CronExpr: "0 9 * * 1",
		}, adminActor())
		require.NoError(t, err)
		// Monday 2026-08-10 09:00 UTC — the expression is interpreted in UTC,
		// not the process-local timezone.
		want := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
		require.Equal(t, want, task.NextFireAt)
		require.Equal(t, "user-1", task.CreatedBy)
		require.Equal(t, 1, len(repo.inserted))
		require.Equal(t, "task-1", repo.inserted[0].ID)
	})

	t.Run("supports cron descriptor with UTC semantics", func(t *testing.T) {
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "daily", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "@daily",
		}, adminActor())
		require.NoError(t, err)
		// @daily fires at 00:00 UTC.
		require.Equal(t, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), repo.inserted[len(repo.inserted)-1].NextFireAt)
	})

	t.Run("propagates persist failure", func(t *testing.T) {
		repo.insertErr = errors.New("db down")
		_, err := svc.Create(context.Background(), testTenant, CreateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorContains(t, err, "scheduler create")
		repo.insertErr = nil
	})
}

func TestServiceUpdatePreservesHistoryAndRevalidates(t *testing.T) {
	repo := &fakeRepo{}
	runner := &fakeRunner{}
	resolver := &fakeResolver{definitionID: "wf-1"}
	svc := newTestService(repo, runner, resolver, func() time.Time { return fixedNow })

	existing := dueTask("task-1", fixedNow.Add(time.Hour))
	lastRun := fixedNow.Add(-time.Hour)
	existing.LastRunAt = &lastRun
	existing.LastRunStatus = domain.LastRunOK
	existing.LastErrorMessage = ""
	repo.tasks = map[string]*domain.ScheduledTask{"task-1": existing}

	t.Run("returns not found for unknown id", func(t *testing.T) {
		_, err := svc.Update(context.Background(), testTenant, "nope", UpdateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, adminActor())
		require.ErrorIs(t, err, domain.ErrScheduledTaskNotFound)
	})

	t.Run("preserves ownership and run history", func(t *testing.T) {
		updated, err := svc.Update(context.Background(), testTenant, "task-1", UpdateCommand{
			Name: "renamed", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{"task": "new"}, CronExpr: "0 8 * * *",
		}, adminActor())
		require.NoError(t, err)
		require.Equal(t, "user-1", updated.CreatedBy)
		require.Equal(t, lastRun, *updated.LastRunAt)
		require.Equal(t, domain.LastRunOK, updated.LastRunStatus)
		require.Equal(t, "renamed", repo.updated[0].Name)
		require.True(t, repo.updated[0].Enabled, "update must re-enable the task")
	})

	t.Run("rejects member actor", func(t *testing.T) {
		_, err := svc.Update(context.Background(), testTenant, "task-1", UpdateCommand{
			Name: "n", WorkflowID: "wf-1", VersionID: "ver-1",
			InputTemplate: map[string]any{}, CronExpr: "0 9 * * *",
		}, Actor{UserID: "user-1", Role: "member"})
		require.ErrorIs(t, err, domain.ErrScheduledTaskForbidden)
	})
}

func TestServiceSetEnabledRecomputesNextFire(t *testing.T) {
	repo := &fakeRepo{}
	svc := newTestService(repo, &fakeRunner{}, &fakeResolver{definitionID: "wf-1"}, func() time.Time { return fixedNow })
	repo.tasks = map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", fixedNow.Add(-time.Hour))}

	t.Run("disable keeps next_fire_at nil", func(t *testing.T) {
		require.NoError(t, svc.SetEnabled(context.Background(), testTenant, "task-1", false, adminActor()))
		require.Equal(t, 1, len(repo.enabledCalls))
		require.False(t, repo.enabledCalls[0].enabled)
		require.Nil(t, repo.enabledCalls[0].next)
	})

	t.Run("re-enable recomputes next fire from now", func(t *testing.T) {
		require.NoError(t, svc.SetEnabled(context.Background(), testTenant, "task-1", true, adminActor()))
		call := repo.enabledCalls[1]
		require.True(t, call.enabled)
		require.NotNil(t, call.next)
		want := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
		require.Equal(t, want, *call.next)
	})

	t.Run("rejects member actor", func(t *testing.T) {
		err := svc.SetEnabled(context.Background(), testTenant, "task-1", true, Actor{UserID: "user-1", Role: "member"})
		require.ErrorIs(t, err, domain.ErrScheduledTaskForbidden)
	})
}

func TestServiceDeleteRequiresAdmin(t *testing.T) {
	repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", fixedNow)}}
	svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })

	require.ErrorIs(t, svc.Delete(context.Background(), testTenant, "task-1", Actor{UserID: "user-1", Role: "member"}), domain.ErrScheduledTaskForbidden)
	require.Empty(t, repo.deleted)

	require.NoError(t, svc.Delete(context.Background(), testTenant, "task-1", adminActor()))
	require.Equal(t, []string{"task-1"}, repo.deleted)
}

func TestServiceListClampsPagination(t *testing.T) {
	repo := &fakeRepo{listTotal: 3}
	svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })

	// 合法 pageSize 原样透传。
	_, total, err := svc.List(context.Background(), testTenant, 1, 5)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, 5, repo.listLimit)
	require.Equal(t, 0, repo.listOffset)

	// 超出上限 clamp 到 DefaultPageSize。
	_, _, err = svc.List(context.Background(), testTenant, 2, constants.MaxPageSize+10)
	require.NoError(t, err)
	require.Equal(t, constants.DefaultPageSize, repo.listLimit)
	require.Equal(t, constants.DefaultPageSize, repo.listOffset)

	// 非正 pageSize 也 clamp。
	_, _, err = svc.List(context.Background(), testTenant, 1, 0)
	require.NoError(t, err)
	require.Equal(t, constants.DefaultPageSize, repo.listLimit)
	require.Equal(t, 0, repo.listOffset)
}

func TestServiceListFillsReferenceNames(t *testing.T) {
	t.Run("fills workflow and version display names from the resolver", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", fixedNow)}, listTotal: 1}
		resolver := &fakeResolver{names: map[string]port.VersionName{
			"ver-1": {WorkflowName: "日报生成", VersionNo: 2, VersionName: "稳定版"},
		}}
		svc := newTestService(repo, &fakeRunner{}, resolver, func() time.Time { return fixedNow })

		got, total, err := svc.List(context.Background(), testTenant, 1, 20)
		require.NoError(t, err)
		require.Equal(t, 1, total)
		require.Len(t, got, 1)
		require.Equal(t, "日报生成", got[0].WorkflowName)
		require.Equal(t, int64(2), got[0].VersionNo)
		require.Equal(t, "稳定版", got[0].VersionName)
	})

	t.Run("keeps raw ids for deleted versions without failing the list", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", fixedNow)}, listTotal: 1}
		svc := newTestService(repo, &fakeRunner{}, &fakeResolver{names: map[string]port.VersionName{}}, func() time.Time { return fixedNow })

		got, _, err := svc.List(context.Background(), testTenant, 1, 20)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Empty(t, got[0].WorkflowName)
		require.Empty(t, got[0].VersionName)
	})

	t.Run("propagates resolver errors", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", fixedNow)}, listTotal: 1}
		resolver := &fakeResolver{namesErr: errors.New("version lookup failed")}
		svc := newTestService(repo, &fakeRunner{}, resolver, func() time.Time { return fixedNow })

		_, _, err := svc.List(context.Background(), testTenant, 1, 20)
		require.ErrorContains(t, err, "version lookup failed")
	})
}

func TestServicePollTenantFailureClassification(t *testing.T) {
	due := fixedNow.Add(-time.Minute)

	t.Run("transient failure does not advance the schedule", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", due)}}
		runner := &fakeRunner{err: errors.New("db down")}
		svc := newTestService(repo, runner, &fakeResolver{}, func() time.Time { return fixedNow })

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.ErrorContains(t, err, "db down")
		require.Empty(t, repo.records, "transient failures must not record a fire")
	})

	t.Run("deterministic failure advances and records error", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", due)}}
		runner := &fakeRunner{err: port.ErrDeterministicFailure}
		svc := newTestService(repo, runner, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = true

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.NoError(t, err)
		require.Equal(t, 1, len(repo.records))
		rec := repo.records[0]
		require.Equal(t, domain.LastRunError, rec.status)
		require.Equal(t, "deterministic scheduled fire failure", rec.errorMsg)
		require.Equal(t, due, rec.oldNext)
		// next fire is one day after the deterministic failure.
		require.True(t, rec.newNext.After(fixedNow))
	})

	t.Run("success advances with ok status", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", due)}}
		runner := &fakeRunner{}
		svc := newTestService(repo, runner, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = true

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.NoError(t, err)
		rec := repo.records[0]
		require.Equal(t, domain.LastRunOK, rec.status)
		require.Empty(t, rec.errorMsg)
		require.Equal(t, due, rec.oldNext)
		wantNext := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
		require.Equal(t, wantNext, rec.newNext)
	})

	t.Run("broken cron cools down for a day with error record", func(t *testing.T) {
		broken := dueTask("task-1", due)
		broken.CronExpr = "not a cron"
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": broken}}
		svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = true

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.NoError(t, err)
		rec := repo.records[0]
		require.Equal(t, domain.LastRunError, rec.status)
		require.Contains(t, rec.errorMsg, "invalid cron")
		require.Equal(t, fixedNow.Add(constants.SchedulerCronBrokenCooldown), rec.newNext)
	})

	t.Run("idempotency key uses the row's next_fire_at", func(t *testing.T) {
		rowNext := due.Add(-30 * time.Second) // 与重新计算的值故意不同
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", rowNext)}}
		runner := &fakeRunner{}
		svc := newTestService(repo, runner, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = true

		require.NoError(t, svc.PollTenant(context.Background(), testTenant, fixedNow))
		require.Equal(t, 1, len(runner.starts))
		start := runner.starts[0]
		require.Equal(t, testTenant, start.tenantID)
		require.Equal(t, "ver-1", start.versionID)
		require.Equal(t, "task-1@"+rowNext.UTC().Format(time.RFC3339), start.key)
		require.Equal(t, "scheduler:task-1", start.createdBy)
	})

	t.Run("concurrent loser skips silently", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", due)}}
		svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = false

		require.NoError(t, svc.PollTenant(context.Background(), testTenant, fixedNow))
	})

	t.Run("record fire storage error propagates", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{"task-1": dueTask("task-1", due)}}
		svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })
		repo.recordFireAdvanced = true
		repo.recordFireErr = errors.New("disk full")

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.ErrorContains(t, err, "scheduler record fire")
	})

	t.Run("list due failure aborts the pass", func(t *testing.T) {
		repo := &fakeRepo{dueErr: errors.New("db down")}
		svc := newTestService(repo, &fakeRunner{}, &fakeResolver{}, func() time.Time { return fixedNow })

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.ErrorContains(t, err, "scheduler list due")
	})

	t.Run("one bad task does not hide other fires", func(t *testing.T) {
		repo := &fakeRepo{tasks: map[string]*domain.ScheduledTask{
			"task-1": dueTask("task-1", due),
			"task-2": dueTask("task-2", due),
		}}
		runner := &fakeRunner{err: errors.New("transient")}
		svc := newTestService(repo, runner, &fakeResolver{}, func() time.Time { return fixedNow })

		err := svc.PollTenant(context.Background(), testTenant, fixedNow)
		require.ErrorContains(t, err, "scheduler fire task-1")
		require.ErrorContains(t, err, "scheduler fire task-2")
		require.Equal(t, 2, len(runner.starts))
	})
}

func TestFireIDKeyIsUTC(t *testing.T) {
	local := time.Date(2026, 8, 9, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	key := fireIDKey("task-1", local)
	require.Equal(t, "task-1@2026-08-09T12:00:00Z", key)
}
