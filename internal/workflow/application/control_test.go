package application_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

type controlStore struct {
	run       *domain.Run
	approvals []domain.Approval
	effects   []domain.EffectIntent
	events    []domain.Event
}

func (s *controlStore) GetRun(context.Context, string, string) (*domain.Run, error) {
	copy := *s.run
	return &copy, nil
}
func (s *controlStore) ControlRun(_ context.Context, _, _ string, expected int64, status domain.RunStatus, reason string, event domain.Event) error {
	if s.run.Generation != expected {
		return domain.ErrGenerationConflict
	}
	s.run.Status = status
	s.run.Generation++
	s.events = append(s.events, event)
	if status == domain.RunStatusPauseRequested {
		s.run.PauseReason = reason
	}
	if status == domain.RunStatusCancelRequested {
		s.run.CancelReason = reason
	}
	if status == domain.RunStatusManualIntervention {
		s.run.ManualReason = reason
	}
	return nil
}
func (s *controlStore) ListApprovals(context.Context, string, string, bool) ([]domain.Approval, error) {
	return append([]domain.Approval(nil), s.approvals...), nil
}
func (s *controlStore) DecideApproval(_ context.Context, _ string, id string, generation int64, attemptID string, decision domain.ApprovalDecision, actor, comment string, event domain.Event) error {
	for i := range s.approvals {
		if s.approvals[i].ID == id {
			if err := s.approvals[i].Decide(decision, actor, comment, generation, attemptID); err != nil {
				return err
			}
			s.events = append(s.events, event)
			return nil
		}
	}
	return domain.ErrNotFound
}
func (s *controlStore) ListEffectIntents(context.Context, string, string) ([]domain.EffectIntent, error) {
	return append([]domain.EffectIntent(nil), s.effects...), nil
}
func (s *controlStore) ResolveEffect(_ context.Context, _ string, id string, generation int64, action domain.ManualAction, output, actor string, event domain.Event) error {
	for i := range s.effects {
		if s.effects[i].ID == id {
			if s.effects[i].RunGeneration != generation {
				return domain.ErrGenerationConflict
			}
			s.events = append(s.events, event)
			return nil
		}
	}
	return domain.ErrNotFound
}

func TestControlServiceCancelIsPersistentAndIdempotent(t *testing.T) {
	store := &controlStore{run: &domain.Run{ID: "run-1", Status: domain.RunStatusRunning, Generation: 3, CreatedBy: "operator"}}
	svc := application.NewControlService(store, func() string { return "event-1" })
	run, err := svc.Cancel(context.Background(), "tenant-1", "run-1", 3, application.Actor{UserID: "operator", Role: "member"}, "stop")
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCancelRequested, run.Status)
	_, err = svc.Cancel(context.Background(), "tenant-1", "run-1", 4, application.Actor{UserID: "operator", Role: "member"}, "again")
	require.NoError(t, err)
	require.Len(t, store.events, 1)
}

func TestControlServiceResumeCannotBypassPendingApproval(t *testing.T) {
	store := &controlStore{run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, Generation: 4}, approvals: []domain.Approval{{ID: "approval-1", Status: domain.ApprovalStatusPending}}}
	svc := application.NewControlService(store, func() string { return "event-1" })
	_, err := svc.Resume(context.Background(), "tenant-1", "run-1", 4, application.Actor{UserID: "operator", Role: "admin"})
	require.ErrorIs(t, err, domain.ErrApprovalRequired)
}

func TestControlServiceApprovalDecisionAndManualActionsAreFenced(t *testing.T) {
	store := &controlStore{run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, Generation: 5}, approvals: []domain.Approval{*domain.NewApproval("approval-1", "run-1", "node", "attempt", 5, "risk", "high", "safe")}, effects: []domain.EffectIntent{*domain.NewEffectIntent("effect-1", "run-1", "node", "attempt", 5, domain.EffectClassNonIdempotent, "key")}}
	svc := application.NewControlService(store, func() string { return "event-1" })
	require.NoError(t, svc.DecideApproval(context.Background(), "tenant-1", application.DecideApprovalCommand{ApprovalID: "approval-1", RunID: "run-1", AttemptID: "attempt", ExpectedGeneration: 5, Decision: domain.ApprovalDecisionApprove, ActorID: "admin", ActorRole: "admin"}))
	require.ErrorIs(t, svc.DecideApproval(context.Background(), "tenant-1", application.DecideApprovalCommand{ApprovalID: "approval-1", RunID: "run-1", AttemptID: "attempt", ExpectedGeneration: 5, Decision: domain.ApprovalDecisionReject, ActorID: "admin", ActorRole: "admin"}), domain.ErrDecisionConflict)
	for _, action := range []domain.ManualAction{domain.ManualActionMarkSucceeded, domain.ManualActionRetry, domain.ManualActionTerminate} {
		require.NoError(t, svc.ResolveManual(context.Background(), "tenant-1", application.ResolveManualCommand{RunID: "run-1", EffectIntentID: "effect-1", ExpectedGeneration: 5, Action: action, ActorID: "admin", ActorRole: "admin", OutputSummary: "reviewed"}))
	}
}

func TestControlServiceCannotResurrectTerminalRun(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunStatusCompleted, domain.RunStatusFailed, domain.RunStatusCanceled} {
		store := &controlStore{run: &domain.Run{ID: "run-1", Status: status, Generation: 9}}
		svc := application.NewControlService(store, func() string { return "event" })
		_, err := svc.Cancel(context.Background(), "tenant", "run-1", 9, application.Actor{UserID: "admin", Role: "admin"}, "late")
		require.ErrorIs(t, err, domain.ErrInvalidTransition)
		_, err = svc.Pause(context.Background(), "tenant", "run-1", 9, application.Actor{UserID: "admin", Role: "admin"}, "late")
		require.ErrorIs(t, err, domain.ErrInvalidTransition)
		require.Equal(t, status, store.run.Status)
	}
}

func TestControlServiceRejectsMalformedApprovalDecisionBeforeStore(t *testing.T) {
	store := &controlStore{run: &domain.Run{ID: "run-1", Status: domain.RunStatusPaused, Generation: 5}, approvals: []domain.Approval{*domain.NewApproval("approval", "run-1", "node", "attempt", 5, "risk", "high", "safe")}}
	svc := application.NewControlService(store, func() string { return "event" })
	err := svc.DecideApproval(context.Background(), "tenant", application.DecideApprovalCommand{ApprovalID: "approval", RunID: "run-1", AttemptID: "attempt", ExpectedGeneration: 5, Decision: "approve ", ActorID: "admin"})
	require.ErrorIs(t, err, domain.ErrInvalidSpec)
	require.Equal(t, domain.ApprovalStatusPending, store.approvals[0].Status)
}
