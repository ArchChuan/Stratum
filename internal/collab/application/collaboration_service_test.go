package application_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/application"
	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/stretchr/testify/require"
)

// collabStore is an in-memory fake for the collab repos, shared by the
// service, plan and worker tests of this package.
type collabStore struct {
	collabs         map[string]*domain.Collaboration
	steps           []domain.TaskStep
	shared          map[string]*domain.SharedContext
	insertedSteps   []domain.TaskStep
	statusUpdates   []collabStatusUpdate
	renewCalls      atomic.Int64
	cancelPendingID string
	conflictOn      map[string]bool // planID -> next Update returns ErrCollabConflict
	claimErr        error
	claimOrder      []string
}

type collabStatusUpdate struct {
	tenantID, id string
	status       domain.CollabStatus
	startedAt    *time.Time
	completedAt  *time.Time
}

func newCollabStore() *collabStore {
	return &collabStore{
		collabs:    map[string]*domain.Collaboration{},
		shared:     map[string]*domain.SharedContext{},
		conflictOn: map[string]bool{},
	}
}

func (s *collabStore) Insert(_ context.Context, collab domain.Collaboration) error {
	cp := collab
	s.collabs[collab.ID] = &cp
	return nil
}

func (s *collabStore) GetByID(_ context.Context, _, id string) (*domain.Collaboration, error) {
	if c, ok := s.collabs[id]; ok {
		cp := *c
		return &cp, nil
	}
	return nil, nil
}

func (s *collabStore) ListByTenant(_ context.Context, tenantID string, limit, offset int) ([]domain.Collaboration, error) {
	var out []domain.Collaboration
	for _, c := range s.collabs {
		if c.TenantID == tenantID {
			out = append(out, *c)
		}
	}
	if offset >= len(out) {
		return nil, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (s *collabStore) UpdateStatus(_ context.Context, tenantID, id string, status domain.CollabStatus, startedAt, completedAt *time.Time) error {
	s.statusUpdates = append(s.statusUpdates, collabStatusUpdate{tenantID: tenantID, id: id, status: status, startedAt: startedAt, completedAt: completedAt})
	c, ok := s.collabs[id]
	if !ok || (c.Status != domain.CollabCreated && c.Status != domain.CollabRunning) {
		// rows affected == 0: terminal plans are never rewritten, mirror the repo.
		return nil
	}
	c.Status = status
	c.StartedAt = startedAt
	c.CompletedAt = completedAt
	return nil
}

// stepRepo adapts collabStore to port.TaskStepRepo: its UpdateStatus has the
// task-step signature, distinct from the collab-plan UpdateStatus.
type stepRepo struct{ *collabStore }

func (s *collabStore) stepsRepo() *stepRepo { return &stepRepo{s} }

func (s *stepRepo) InsertBatch(_ context.Context, _ string, steps []domain.TaskStep) error {
	s.collabStore.insertedSteps = append(s.collabStore.insertedSteps, steps...)
	return nil
}

func (s *stepRepo) ClaimNextTask(_ context.Context, owner string, lease time.Duration) (string, *domain.TaskStep, bool, error) {
	if s.claimErr != nil {
		return "", nil, false, s.claimErr
	}
	for i := range s.steps {
		step := &s.steps[i]
		if step.Status == domain.TaskPending {
			now := time.Now()
			step.Status = domain.TaskClaimed
			step.ClaimedBy = owner
			step.Generation++
			leaseExpiry := now.Add(lease)
			step.LeaseExpiresAt = &leaseExpiry
			s.claimOrder = append(s.claimOrder, step.ID)
			return "tenant-1", step, true, nil
		}
	}
	return "", nil, false, nil
}

func (s *stepRepo) RenewLease(_ context.Context, _, _, _ string, _ time.Duration) error {
	s.renewCalls.Add(1)
	return nil
}

func (s *stepRepo) UpdateStatus(_ context.Context, _ string, stepID string, expectedGeneration int64, status domain.TaskStatus, output map[string]any, errMsg string) error {
	for i := range s.steps {
		if s.steps[i].ID != stepID {
			continue
		}
		if s.steps[i].Generation != expectedGeneration {
			return domain.ErrCollabConflict
		}
		s.steps[i].Status = status
		s.steps[i].Output = output
		s.steps[i].Error = errMsg
		if status == domain.TaskPending {
			s.steps[i].RetryCount++
		}
		return nil
	}
	return nil
}

func (s *stepRepo) GetReadyTasks(_ context.Context, _ string, planID string) ([]domain.TaskStep, error) {
	var out []domain.TaskStep
	for _, st := range s.steps {
		if st.PlanID == planID {
			out = append(out, st)
		}
	}
	return out, nil
}

func (s *stepRepo) CancelPending(_ context.Context, _ string, planID string) error {
	s.cancelPendingID = planID
	for i := range s.steps {
		if s.steps[i].PlanID == planID && s.steps[i].Status == domain.TaskPending {
			s.steps[i].Status = domain.TaskCanceled
		}
	}
	return nil
}

func (s *stepRepo) CountByStatus(_ context.Context, _ string, planID string) (map[domain.TaskStatus]int, error) {
	counts := map[domain.TaskStatus]int{}
	for _, st := range s.steps {
		if st.PlanID == planID {
			counts[st.Status]++
		}
	}
	return counts, nil
}

func (s *collabStore) Get(_ context.Context, _ string, planID string) (*domain.SharedContext, error) {
	if sc, ok := s.shared[planID]; ok {
		cp := *sc
		return &cp, nil
	}
	return nil, nil
}

func (s *collabStore) Update(_ context.Context, _ string, sc domain.SharedContext) error {
	if s.conflictOn[sc.PlanID] {
		delete(s.conflictOn, sc.PlanID)
		return domain.ErrCollabConflict
	}
	cur, ok := s.shared[sc.PlanID]
	if !ok {
		// first writer upserts the row
		cp := sc
		cp.Version = 0
		s.shared[sc.PlanID] = &cp
		return nil
	}
	if cur.Version != sc.Version {
		return domain.ErrCollabConflict
	}
	cp := sc
	cp.Version = sc.Version + 1
	s.shared[sc.PlanID] = &cp
	return nil
}

// fakeMetrics records collab metric calls.
type fakeMetrics struct {
	observability.NoopMetrics
	planOutcomes []string
	taskDuration float64
}

func (m *fakeMetrics) IncCollabPlan(strategy, outcome string) {
	m.planOutcomes = append(m.planOutcomes, strategy+"|"+outcome)
}
func (m *fakeMetrics) RecordCollabTaskDuration(_ string, seconds float64) { m.taskDuration = seconds }

func newTestService(store *collabStore, metrics observability.MetricsProvider) *application.CollaborationService {
	return application.NewCollaborationService(store, store.stepsRepo(), metrics, func() string { return "collab-1" })
}

func fixedActor(role, userID string) application.Actor {
	return application.Actor{Role: role, UserID: userID}
}

func createdPlan(store *collabStore, id, tenantID, createdBy string) {
	store.collabs[id] = &domain.Collaboration{
		ID: id, TenantID: tenantID, Strategy: domain.CollabSequential, Status: domain.CollabCreated,
		CreatedBy: createdBy, Participants: []string{"a1", "a2"}, TaskDescription: "build the report",
	}
}

func TestCreateValidatesInput(t *testing.T) {
	tests := []struct {
		name      string
		desc      string
		strategy  domain.CollabStrategy
		parts     []string
		wantError bool
	}{
		{"valid", "task", domain.CollabParallel, []string{"a1", "a2"}, false},
		{"empty description", "  ", domain.CollabParallel, []string{"a1"}, true},
		{"unknown strategy", "task", "chaotic", []string{"a1"}, true},
		{"no participants", "task", domain.CollabParallel, nil, true},
		{"empty participant id", "task", domain.CollabParallel, []string{"a1", " "}, true},
		{"too many participants", "task", domain.CollabParallel, make([]string, 17), true},
		{"duplicate participants deduped", "task", domain.CollabParallel, []string{"a1", "a2", "a1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newCollabStore()
			metrics := &fakeMetrics{}
			svc := newTestService(store, metrics)
			collab, err := svc.Create(context.Background(), "tenant-1", fixedActor("member", "creator"), tc.desc, tc.strategy, tc.parts)
			if tc.wantError {
				require.Error(t, err)
				require.ErrorIs(t, err, domain.ErrCollabInvalidInput)
				return
			}
			require.NoError(t, err)
			require.Equal(t, domain.CollabCreated, collab.Status)
			require.Equal(t, "creator", collab.CreatedBy)
			if tc.name == "duplicate participants deduped" {
				require.Equal(t, []string{"a1", "a2"}, collab.Participants)
			}
			require.Equal(t, []string{"parallel|created"}, metrics.planOutcomes)
		})
	}
}

func TestControlActionsAuthorizeByRole(t *testing.T) {
	// plan-1 created by "creator"; plan-2 created by "other".
	setup := func() *collabStore {
		store := newCollabStore()
		createdPlan(store, "plan-1", "tenant-1", "creator")
		createdPlan(store, "plan-2", "tenant-1", "other")
		return store
	}
	tests := []struct {
		name       string
		actor      application.Actor
		plan       string
		action     string // start | cancel
		wantError  bool
		wantStatus domain.CollabStatus
	}{
		{"admin starts any plan", fixedActor("admin", "admin-1"), "plan-1", "start", false, domain.CollabRunning},
		{"owner starts any plan", fixedActor("owner", "owner-1"), "plan-2", "start", false, domain.CollabRunning},
		{"creator starts own plan", fixedActor("member", "creator"), "plan-1", "start", false, domain.CollabRunning},
		{"creator cancels own plan", fixedActor("member", "creator"), "plan-1", "cancel", false, domain.CollabCanceled},
		{"other member sees not-found", fixedActor("member", "stranger"), "plan-1", "start", true, ""},
		{"other member cancel sees not-found", fixedActor("member", "stranger"), "plan-1", "cancel", true, ""},
		{"unauthenticated forbidden", fixedActor("member", ""), "plan-1", "start", true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := setup()
			svc := newTestService(store, &fakeMetrics{})
			var err error
			switch tc.action {
			case "start":
				_, err = svc.Start(context.Background(), "tenant-1", tc.plan, tc.actor)
			case "cancel":
				err = svc.Cancel(context.Background(), "tenant-1", tc.plan, tc.actor)
			}
			if tc.wantError {
				require.Error(t, err)
				if tc.actor.UserID == "" {
					require.ErrorIs(t, err, domain.ErrCollabForbidden)
				} else {
					require.ErrorIs(t, err, domain.ErrCollabNotFound)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, store.collabs[tc.plan].Status)
		})
	}
}

func TestCancelReleasesPendingSteps(t *testing.T) {
	store := newCollabStore()
	createdPlan(store, "plan-1", "tenant-1", "creator")
	store.steps = []domain.TaskStep{
		{ID: "s1", PlanID: "plan-1", Status: domain.TaskPending},
		{ID: "s2", PlanID: "plan-1", Status: domain.TaskCompleted},
		{ID: "s3", PlanID: "plan-2", Status: domain.TaskPending},
	}
	svc := newTestService(store, &fakeMetrics{})
	require.NoError(t, svc.Cancel(context.Background(), "tenant-1", "plan-1", fixedActor("admin", "admin-1")))
	require.Equal(t, domain.CollabCanceled, store.collabs["plan-1"].Status)
	require.Equal(t, "plan-1", store.cancelPendingID)
	require.Equal(t, domain.TaskCanceled, store.steps[0].Status)
	require.Equal(t, domain.TaskCompleted, store.steps[1].Status)
	require.Equal(t, domain.TaskPending, store.steps[2].Status, "other plan steps untouched")
}

func TestStartRejectsInvalidTransition(t *testing.T) {
	store := newCollabStore()
	createdPlan(store, "plan-1", "tenant-1", "creator")
	store.collabs["plan-1"].Status = domain.CollabCompleted
	svc := newTestService(store, &fakeMetrics{})
	_, err := svc.Start(context.Background(), "tenant-1", "plan-1", fixedActor("admin", "admin-1"))
	require.ErrorIs(t, err, domain.ErrCollabInvalidTransition)
}

func TestGetAndListRequireMember(t *testing.T) {
	store := newCollabStore()
	createdPlan(store, "plan-1", "tenant-1", "creator")
	svc := newTestService(store, &fakeMetrics{})

	_, err := svc.Get(context.Background(), "tenant-1", "plan-1", fixedActor("member", ""))
	require.ErrorIs(t, err, domain.ErrCollabForbidden)
	_, err = svc.List(context.Background(), "tenant-1", fixedActor("member", ""), 10, 0)
	require.ErrorIs(t, err, domain.ErrCollabForbidden)

	collab, err := svc.Get(context.Background(), "tenant-1", "plan-1", fixedActor("member", "anyone"))
	require.NoError(t, err)
	require.Equal(t, "plan-1", collab.ID)

	_, err = svc.Get(context.Background(), "tenant-1", "missing", fixedActor("member", "anyone"))
	require.ErrorIs(t, err, domain.ErrCollabNotFound)
}
