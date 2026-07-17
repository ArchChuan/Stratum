package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
)

type ControlService struct {
	store port.ControlRepository
	newID func() string
}

func NewControlService(store port.ControlRepository, newID func() string) *ControlService {
	return &ControlService{store: store, newID: newID}
}

func (s *ControlService) event(runID, eventType, actor string) domain.Event {
	return domain.Event{ID: s.newID(), RunID: runID, Type: eventType, ActorType: "human", ActorID: actor, Payload: map[string]any{"actor_id": actor}, OccurredAt: time.Now().UTC()}
}

func (s *ControlService) Cancel(ctx context.Context, tenantID, runID string, expected int64, actor, reason string) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == domain.RunStatusCancelRequested || run.Status == domain.RunStatusCanceled {
		if run.Status == domain.RunStatusCancelRequested {
			return run, nil
		}
		return nil, domain.ErrInvalidTransition
	}
	if run.Status == domain.RunStatusCompleted || run.Status == domain.RunStatusFailed {
		return nil, domain.ErrInvalidTransition
	}
	if err := s.store.ControlRun(ctx, tenantID, runID, expected, domain.RunStatusCancelRequested, reason, s.event(runID, "workflow.cancel_requested", actor)); err != nil {
		return nil, err
	}
	return s.store.GetRun(ctx, tenantID, runID)
}

func (s *ControlService) Pause(ctx context.Context, tenantID, runID string, expected int64, actor, reason string) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.Generation != expected {
		return nil, domain.ErrGenerationConflict
	}
	if run.Status != domain.RunStatusQueued && run.Status != domain.RunStatusRunning {
		return nil, domain.ErrInvalidTransition
	}
	if err := s.store.ControlRun(ctx, tenantID, runID, expected, domain.RunStatusPauseRequested, reason, s.event(runID, "workflow.pause_requested", actor)); err != nil {
		return nil, err
	}
	return s.store.GetRun(ctx, tenantID, runID)
}

func (s *ControlService) Resume(ctx context.Context, tenantID, runID string, expected int64, actor string) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if run.Generation != expected {
		return nil, domain.ErrGenerationConflict
	}
	approvals, err := s.store.ListApprovals(ctx, tenantID, runID, true)
	if err != nil {
		return nil, err
	}
	if len(approvals) > 0 {
		return nil, domain.ErrApprovalRequired
	}
	intents, err := s.store.ListEffectIntents(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	for _, intent := range intents {
		if intent.RequiresManualIntervention() {
			return nil, domain.ErrInvalidTransition
		}
	}
	if run.Status != domain.RunStatusPaused && run.Status != domain.RunStatusManualIntervention {
		return nil, domain.ErrInvalidTransition
	}
	if err := s.store.ControlRun(ctx, tenantID, runID, expected, domain.RunStatusQueued, "", s.event(runID, "workflow.resumed", actor)); err != nil {
		return nil, err
	}
	return s.store.GetRun(ctx, tenantID, runID)
}

type DecideApprovalCommand struct {
	ApprovalID, RunID, AttemptID string
	ExpectedGeneration           int64
	Decision                     domain.ApprovalDecision
	ActorID, Comment             string
}

func (s *ControlService) DecideApproval(ctx context.Context, tenantID string, cmd DecideApprovalCommand) error {
	if cmd.ActorID == "" {
		return fmt.Errorf("%w: decision actor is required", domain.ErrInvalidSpec)
	}
	if cmd.Decision != domain.ApprovalDecisionApprove && cmd.Decision != domain.ApprovalDecisionReject {
		return fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalidSpec)
	}
	event := s.event(cmd.RunID, "workflow.approval_decided", cmd.ActorID)
	event.Payload["decision"] = string(cmd.Decision)
	return s.store.DecideApproval(ctx, tenantID, cmd.ApprovalID, cmd.ExpectedGeneration, cmd.AttemptID, cmd.Decision, cmd.ActorID, cmd.Comment, event)
}

type ResolveManualCommand struct {
	RunID, EffectIntentID  string
	ExpectedGeneration     int64
	Action                 domain.ManualAction
	OutputSummary, ActorID string
}

func (s *ControlService) ResolveManual(ctx context.Context, tenantID string, cmd ResolveManualCommand) error {
	if cmd.ActorID == "" {
		return fmt.Errorf("%w: actor is required", domain.ErrInvalidSpec)
	}
	event := s.event(cmd.RunID, "workflow.manual_intervention_resolved", cmd.ActorID)
	event.Payload["action"] = string(cmd.Action)
	return s.store.ResolveEffect(ctx, tenantID, cmd.EffectIntentID, cmd.ExpectedGeneration, cmd.Action, cmd.OutputSummary, cmd.ActorID, event)
}

func (s *ControlService) AvailableActions(ctx context.Context, tenantID, runID string) ([]string, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	approvals, err := s.store.ListApprovals(ctx, tenantID, runID, true)
	if err != nil {
		return nil, err
	}
	intents, err := s.store.ListEffectIntents(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	manual := false
	for _, i := range intents {
		manual = manual || i.RequiresManualIntervention()
	}
	return run.AvailableActions(len(approvals) > 0, manual), nil
}

func (s *ControlService) ListApprovals(ctx context.Context, tenantID, runID string, pending bool) ([]domain.Approval, error) {
	return s.store.ListApprovals(ctx, tenantID, runID, pending)
}
func (s *ControlService) ListEffects(ctx context.Context, tenantID, runID string) ([]domain.EffectIntent, error) {
	return s.store.ListEffectIntents(ctx, tenantID, runID)
}
