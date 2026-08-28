package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
)

type ControlService struct {
	store          port.ControlRepository
	agentApprovals port.AgentApprovalResolver
	newID          func() string
}

func NewControlService(store port.ControlRepository, newID func() string) *ControlService {
	return &ControlService{store: store, newID: newID}
}

// SetAgentApprovalResolver 注入 agent 原生审批判定器，Resume 时挡住 agent 审批
// 仍未决的 run（避免审批前放行重跑）。由 wiring 装配时调用。
func (s *ControlService) SetAgentApprovalResolver(resolver port.AgentApprovalResolver) {
	s.agentApprovals = resolver
}

func (s *ControlService) event(runID, eventType string, actor Actor) domain.Event {
	return domain.Event{ID: s.newID(), RunID: runID, Type: eventType, ActorType: "human", ActorID: actor.UserID, Payload: map[string]any{"actor_id": actor.UserID}, OccurredAt: time.Now().UTC()}
}

func (s *ControlService) Cancel(ctx context.Context, tenantID, runID string, expected int64, actor Actor, reason string) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionCancel); err != nil {
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

func (s *ControlService) Pause(ctx context.Context, tenantID, runID string, expected int64, actor Actor, reason string) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionPause); err != nil {
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

func (s *ControlService) Resume(ctx context.Context, tenantID, runID string, expected int64, actor Actor) (*domain.Run, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionResume); err != nil {
		return nil, err
	}
	if run.Generation != expected {
		return nil, domain.ErrGenerationConflict
	}
	if err := s.ensureResumable(ctx, tenantID, runID); err != nil {
		return nil, err
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
	ActorID, ActorRole, Comment  string
}

func (s *ControlService) DecideApproval(ctx context.Context, tenantID string, cmd DecideApprovalCommand) error {
	if cmd.ActorID == "" {
		return fmt.Errorf("%w: decision actor is required", domain.ErrInvalidSpec)
	}
	if cmd.Decision != domain.ApprovalDecisionApprove && cmd.Decision != domain.ApprovalDecisionReject {
		return fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalidSpec)
	}
	actor := Actor{UserID: cmd.ActorID, Role: cmd.ActorRole}
	run, err := s.store.GetRun(ctx, tenantID, cmd.RunID)
	if err != nil {
		return err
	}
	if err := authorizeRun(run, actor, RunActionApprove); err != nil {
		return err
	}
	event := s.event(cmd.RunID, "workflow.approval_decided", actor)
	event.Payload["decision"] = string(cmd.Decision)
	return s.store.DecideApproval(ctx, tenantID, cmd.ApprovalID, cmd.ExpectedGeneration, cmd.AttemptID, cmd.Decision, cmd.ActorID, cmd.Comment, event)
}

type ResolveManualCommand struct {
	RunID, EffectIntentID             string
	ExpectedGeneration                int64
	Action                            domain.ManualAction
	OutputSummary, ActorID, ActorRole string
}

func (s *ControlService) ResolveManual(ctx context.Context, tenantID string, cmd ResolveManualCommand) error {
	if cmd.ActorID == "" {
		return fmt.Errorf("%w: actor is required", domain.ErrInvalidSpec)
	}
	actor := Actor{UserID: cmd.ActorID, Role: cmd.ActorRole}
	run, err := s.store.GetRun(ctx, tenantID, cmd.RunID)
	if err != nil {
		return err
	}
	if err := authorizeRun(run, actor, RunActionResolveManual); err != nil {
		return err
	}
	event := s.event(cmd.RunID, "workflow.manual_intervention_resolved", actor)
	event.Payload["action"] = string(cmd.Action)
	return s.store.ResolveEffect(ctx, tenantID, cmd.EffectIntentID, cmd.ExpectedGeneration, cmd.Action, cmd.OutputSummary, cmd.ActorID, event)
}

func (s *ControlService) AvailableActions(ctx context.Context, tenantID, runID string, actor Actor) ([]string, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionRead); err != nil {
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
	actions := run.AvailableActions(len(approvals) > 0, manual)
	if actor.Role != "admin" && actor.Role != "owner" {
		return creatorVisibleActions(run, actor.UserID, actions), nil
	}
	return actions, nil
}

// agentApprovalPending 判断 run 是否存在 agent 原生审批仍 pending 的暂停节点。
// Resume 前的双保险：agent 审批未决时拒绝恢复，等 reconcile 下 tick 再判。
func (s *ControlService) agentApprovalPending(ctx context.Context, tenantID, runID string) (bool, error) {
	attempts, ok := s.store.(port.AttemptRepository)
	if !ok {
		return false, fmt.Errorf("workflow attempt repository unavailable")
	}
	rows, err := attempts.ListAttempts(ctx, tenantID, runID)
	if err != nil {
		return false, err
	}
	for _, attempt := range rows {
		if attempt.Status != domain.AttemptStatusPaused || attempt.ErrorCode != "agent_approval_required" {
			continue
		}
		done, err := s.agentApprovals.ResolveAgentApproval(ctx, tenantID, deterministicExecutionID(runID, attempt.NodeID))
		if err != nil {
			return false, err
		}
		if !done {
			return true, nil
		}
	}
	return false, nil
}

// ensureResumable 校验 run 可被恢复：无待决人工审批，且 agent 原生审批（若注入
// resolver）无 pending 节点。任一未决即返回 ErrApprovalRequired。
func (s *ControlService) ensureResumable(ctx context.Context, tenantID, runID string) error {
	approvals, err := s.store.ListApprovals(ctx, tenantID, runID, true)
	if err != nil {
		return err
	}
	if len(approvals) > 0 {
		return domain.ErrApprovalRequired
	}
	if s.agentApprovals == nil {
		return nil
	}
	blocked, err := s.agentApprovalPending(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if blocked {
		return domain.ErrApprovalRequired
	}
	return nil
}

// creatorVisibleActions 发起人只保留运行控制动作（暂停/继续/取消）；审批与人工
// 干预动作仍由 admin/owner 专属。非发起人无可保留动作时返回 nil。
func creatorVisibleActions(run *domain.Run, userID string, actions []string) []string {
	if run.CreatedBy != userID {
		return nil
	}
	var kept []string
	for _, action := range actions {
		if action == "pause" || action == "resume" || action == "cancel" {
			kept = append(kept, action)
		}
	}
	return kept
}

func (s *ControlService) ListApprovals(ctx context.Context, tenantID, runID string, actor Actor, pending bool) ([]domain.Approval, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionRead); err != nil {
		return nil, err
	}
	return s.store.ListApprovals(ctx, tenantID, runID, pending)
}
func (s *ControlService) ListEffects(ctx context.Context, tenantID, runID string, actor Actor) ([]domain.EffectIntent, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionRead); err != nil {
		return nil, err
	}
	return s.store.ListEffectIntents(ctx, tenantID, runID)
}
