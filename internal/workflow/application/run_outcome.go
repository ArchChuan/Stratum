package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
)

// commitOutcome 按执行结果分派持久化：暂停、失败、成功三条路径。
func (s *RunService) commitOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	if outcome.err == nil && outcome.result.Paused {
		return s.commitPausedOutcome(ctx, tenantID, run, outcome)
	}
	if outcome.err != nil {
		return s.commitFailedOutcome(ctx, tenantID, run, outcome)
	}
	return s.commitSucceededOutcome(ctx, tenantID, run, outcome)
}

// commitPausedOutcome 持久化暂停结果：agent 原生审批暂停走 agent 通道（不建
// workflow_approvals，复用 tool_approvals 审批流），其余 approval 节点走创建审批。
func (s *RunService) commitPausedOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	if outcome.result.ErrorCode == "agent_approval_required" {
		return s.commitAgentApprovalPausedOutcome(ctx, tenantID, run, outcome)
	}
	attempt := outcome.attempt
	attempt.Status = domain.AttemptStatusPaused
	attempt.OutputSummary = outcome.result.Output
	if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_paused", "approval required"); err != nil {
		return err
	}
	approvals, ok := s.store.(port.ApprovalRepository)
	if !ok {
		return fmt.Errorf("workflow approval repository unavailable")
	}
	reason, risk := "human approval required", "high"
	approval := domain.NewApproval(s.newID(), run.ID, attempt.NodeID, attempt.ID, outcome.approvalGeneration+1, reason, risk, attempt.Input)
	if err := approvals.CreateApproval(ctx, tenantID, approval, domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.approval_requested", NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, Status: string(domain.ApprovalStatusPending), Summary: reason, OccurredAt: time.Now().UTC()}); err != nil {
		return err
	}
	run.Status, run.PauseReason, run.Generation = domain.RunStatusPaused, reason, approval.RunGeneration
	return nil
}

// commitAgentApprovalPausedOutcome 持久化 agent 原生审批暂停：attempt 落 paused +
// ErrorCode 标记（reconcile 靠它识别恢复时机），run 经 controller 收敛到 paused。
// 不创建 workflow_approvals —— 审批实体是 agent 侧 tool_approvals，用户在 agent
// 审批中心处理，批准后重跑经 deterministicExecutionID 续跑。
func (s *RunService) commitAgentApprovalPausedOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	attempt := outcome.attempt
	attempt.Status = domain.AttemptStatusPaused
	attempt.ErrorCode = "agent_approval_required"
	attempt.OutputSummary = outcome.result.Output
	if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_paused", "agent approval required"); err != nil {
		return err
	}
	event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.paused", Status: string(domain.RunStatusPaused), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, Summary: "agent approval required", OccurredAt: time.Now().UTC()}
	return s.commitBoundaryStatus(ctx, tenantID, run, event, domain.RunStatusPaused, "agent approval required", run.Generation)
}

// effectStarted 报告执行是否已启动 effect（失败路径的 effect 状态决策）。
func (o executionOutcome) effectStarted() bool {
	return o.effect != nil && o.effect.Status == domain.EffectIntentStatusStarted
}

// commitFailedOutcome 持久化失败执行：先处理 effect 状态，再处理取消语义，
// 最后落到重试或失败。
func (s *RunService) commitFailedOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	if outcome.effectStarted() && outcome.effect.EffectClass == domain.EffectClassNonIdempotent {
		return s.commitEffectUnknown(ctx, tenantID, run, outcome)
	}
	if outcome.effectStarted() {
		if err := s.markEffectFailed(ctx, tenantID, outcome); err != nil {
			return err
		}
	}
	if errors.Is(outcome.err, context.Canceled) {
		handled, err := s.commitCanceledOutcome(ctx, tenantID, run, outcome)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	return s.commitRetryOrFail(ctx, tenantID, outcome)
}

// commitEffectUnknown 将结果未知的非幂等 effect 置为 manual intervention，
// 防止重放副作用。
func (s *RunService) commitEffectUnknown(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	effects := s.store.(port.EffectRepository)
	if err := outcome.effect.MarkUnknown(outcome.err.Error(), run.Generation); err != nil {
		return err
	}
	if err := effects.UpdateEffectIntent(ctx, tenantID, outcome.effect, domain.EffectIntentStatusStarted); err != nil {
		return err
	}
	attempt := outcome.attempt
	attempt.Status, attempt.ErrorMessage = domain.AttemptStatusManualIntervention, outcome.err.Error()
	if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.manual_intervention", run.ManualReason); err != nil {
		return err
	}
	run.Status, run.ManualReason = domain.RunStatusManualIntervention, "external effect result is unknown"
	if err := s.store.UpdateRun(ctx, tenantID, run); err != nil {
		return err
	}
	// UpdateRun 乐观锁成功即 generation+1,内存同步保证同批后续 outcome 的 CAS 基于最新值。
	run.Generation++
	return nil
}

// markEffectFailed 把已启动但结果失败的 effect 标记为 failed。
func (s *RunService) markEffectFailed(ctx context.Context, tenantID string, outcome executionOutcome) error {
	effects := s.store.(port.EffectRepository)
	outcome.effect.Status, outcome.effect.Reason = domain.EffectIntentStatusFailed, outcome.err.Error()
	return effects.UpdateEffectIntent(context.WithoutCancel(ctx), tenantID, outcome.effect, domain.EffectIntentStatusStarted)
}

// commitCanceledOutcome 处理节点边界的取消：先查 fresh run 是否
// pause-requested，未命中再查是否 cancel-requested；都未命中返回
// handled=false 让调用方继续走重试逻辑。两次 GetRun 与原实现逐一对应。
func (s *RunService) commitCanceledOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) (bool, error) {
	fresh, err := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
	if err != nil {
		return false, err
	}
	if fresh.Status == domain.RunStatusPauseRequested {
		return true, s.commitPauseBoundary(ctx, tenantID, run, outcome, fresh)
	}
	fresh, err = s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
	if err != nil {
		return false, err
	}
	if fresh.Status == domain.RunStatusCancelRequested {
		return true, s.commitCancelBoundary(ctx, tenantID, run, outcome, fresh)
	}
	return false, nil
}

// commitPauseBoundary 把节点边界取消落成暂停：checkpoint retry_wait 并收敛
// run 状态。
func (s *RunService) commitPauseBoundary(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome, fresh *domain.Run) error {
	attempt := outcome.attempt
	attempt.Status, attempt.ErrorMessage, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusRetryWait, "paused at node boundary", fresh.Generation, fresh.Generation
	attempt.RetryAt = nil
	if err := s.checkpointAttempt(context.WithoutCancel(ctx), tenantID, attempt, "workflow.node_paused", attempt.ErrorMessage); err != nil {
		return err
	}
	event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.paused", Status: string(domain.RunStatusPaused), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, OccurredAt: time.Now().UTC()}
	return s.commitBoundaryStatus(ctx, tenantID, run, event, domain.RunStatusPaused, fresh.PauseReason, fresh.Generation)
}

// commitCancelBoundary 把节点边界取消落成 canceled：checkpoint canceled 并
// 收敛 run 状态。
func (s *RunService) commitCancelBoundary(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome, fresh *domain.Run) error {
	attempt := outcome.attempt
	attempt.Status, attempt.ErrorMessage, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusCanceled, "canceled", fresh.Generation, fresh.Generation
	if err := s.checkpointAttempt(context.WithoutCancel(ctx), tenantID, attempt, "workflow.node_canceled", "canceled"); err != nil {
		return err
	}
	event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.canceled", Status: string(domain.RunStatusCanceled), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, OccurredAt: time.Now().UTC()}
	return s.commitBoundaryStatus(ctx, tenantID, run, event, domain.RunStatusCanceled, fresh.CancelReason, fresh.Generation)
}

// commitBoundaryStatus 通过 controller 把 run 收敛到目标状态，并在成功后把
// 内存 run 同步为最新值（乐观锁 generation 漂移容错）。
func (s *RunService) commitBoundaryStatus(ctx context.Context, tenantID string, run *domain.Run, event domain.Event, status domain.RunStatus, reason string, generation int64) error {
	controller, ok := s.store.(runController)
	if !ok {
		return fmt.Errorf("workflow control repository unavailable")
	}
	if err := controller.ControlRun(context.WithoutCancel(ctx), tenantID, run.ID, generation, status, reason, event); err != nil {
		return err
	}
	latest, getErr := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
	if getErr == nil {
		*run = *latest
	}
	return nil
}

// commitRetryOrFail 决策重试或失败：满足重试条件则进入 retry_wait，否则落
// failed 并返回包装错误。
func (s *RunService) commitRetryOrFail(ctx context.Context, tenantID string, outcome executionOutcome) error {
	attempt := outcome.attempt
	attempt.ErrorMessage, attempt.ErrorCode = outcome.err.Error(), outcome.result.ErrorCode
	maxAttempts := outcome.node.Retry.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	canRetry := outcome.result.Retryable && attempt.AttemptNo < maxAttempts && outcome.node.EffectClass != domain.EffectClassNonIdempotent
	if canRetry {
		attempt.Status = domain.AttemptStatusRetryWait
		retryAt := time.Now().Add(time.Duration(outcome.node.Retry.BackoffMS) * time.Millisecond)
		attempt.RetryAt = &retryAt
		if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_retrying", attempt.ErrorMessage); err != nil {
			return err
		}
		return nil
	}
	attempt.Status = domain.AttemptStatusFailed
	if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_failed", attempt.ErrorMessage); err != nil {
		return err
	}
	return fmt.Errorf("node %s: %w", attempt.NodeID, outcome.err)
}

// commitSucceededOutcome 持久化成功执行：effect 成功 + 完成 checkpoint。
func (s *RunService) commitSucceededOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	attempt := outcome.attempt
	if outcome.effect != nil {
		effects := s.store.(port.EffectRepository)
		previous := outcome.effect.Status
		outcome.effect.Status, outcome.effect.OutputSummary = domain.EffectIntentStatusSucceeded, outcome.result.Output
		if err := effects.UpdateEffectIntent(ctx, tenantID, outcome.effect, previous); err != nil {
			return err
		}
	}
	attempt.Status = domain.AttemptStatusSucceeded
	if outcome.node.Type == domain.NodeTypeCondition {
		attempt.OutputSummary = strconv.FormatBool(outcome.result.ConditionValue)
		attempt.SelectedEdges = selectedConditionEdges(run.Snapshot, outcome.node.ID, outcome.result.ConditionValue)
	} else {
		mapped, err := applyOutputMapping(outcome.result.Output, outcome.node.OutputMapping)
		if err != nil {
			return fmt.Errorf("node %s output mapping: %w", outcome.node.ID, err)
		}
		attempt.OutputSummary = mapped
	}
	if attempt.OutputSummary == "" {
		attempt.OutputSummary = "{}"
	}
	attempt.TraceID = outcome.result.TraceID
	if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_completed", attempt.OutputSummary); err != nil {
		return err
	}
	return nil
}

func applyOutputMapping(output string, mapping map[string]string) (string, error) {
	if len(mapping) == 0 {
		return output, nil
	}
	var source any
	if err := json.Unmarshal([]byte(output), &source); err != nil {
		return "", err
	}
	mapped := make(map[string]any, len(mapping))
	for key, selector := range mapping {
		if selector == "$" {
			mapped[key] = source
			continue
		}
		value := source
		for _, part := range strings.Split(strings.TrimPrefix(selector, "$."), ".") {
			object, ok := value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("selector %s requires object at %s", selector, part)
			}
			next, exists := object[part]
			if !exists {
				return "", fmt.Errorf("selector %s not found", selector)
			}
			value = next
		}
		mapped[key] = value
	}
	encoded, err := json.Marshal(mapped)
	return string(encoded), err
}

func selectedConditionEdges(spec domain.Spec, nodeID string, value bool) []string {
	selected := make([]string, 0, 1)
	for _, edge := range spec.Edges {
		if edge.From != nodeID || !conditionEdgeSelected(spec, nodeID, edge, value) {
			continue
		}
		id := edge.ID
		if id == "" {
			id = edge.From + "->" + edge.To
		}
		selected = append(selected, id)
	}
	sort.Strings(selected)
	return selected
}
