package graph

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// nodePlanFinalize 是 plan 波次汇合节点：所有已激活槽位完成后执行一次，
// 应用波次结果并恢复 LLM 上下文。
const nodePlanFinalize = "plan-finalize"

// PlanWaveOutcome 是单个 plan 节点在一次波次中的执行结果，由槽位节点产出、
// finalize 节点汇合应用到 ActivePlan。
type PlanWaveOutcome struct {
	NodeID  string
	Status  domain.PlanNodeStatus
	Summary string
	Err     string
}

// makePlanSlotNode 返回 plan 槽位 i（0 起）的节点函数：执行
// plan.Nodes[PlanWavePending[i]] 并记录一个 PlanWaveOutcome。槽位不递增
// Steps——MaxLLMSteps 只统计 LLM 节点调用次数，强制最终回答机制不受
// plan 波次影响。
func makePlanSlotNode(i int) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if s.PlanNodeExecutor == nil {
			return s, fmt.Errorf("plan slot %d: executor is required", i)
		}
		if s.ActivePlan == nil || i >= len(s.PlanWavePending) {
			return s, fmt.Errorf("plan slot %d: no pending node at index %d", i, i)
		}
		idx := s.PlanWavePending[i]
		if idx < 0 || idx >= len(s.ActivePlan.Nodes) {
			return s, fmt.Errorf("plan slot %d: node index %d out of range", i, idx)
		}
		node := s.ActivePlan.Nodes[idx]
		outcome := PlanWaveOutcome{NodeID: node.ID}
		var result PlanNodeExecutionResult
		var execErr error
		// 单节点 panic 恢复语义与旧波次执行器一致：panic 折叠为失败 outcome，
		// 波次继续推进，不中断整图。
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					execErr = fmt.Errorf("node panic: %v", recovered)
				}
			}()
			result, execErr = s.PlanNodeExecutor(ctx, planSlotChildState(s), node, dependencySummaries(s.ActivePlan, node))
		}()
		switch {
		case execErr != nil:
			outcome.Status = domain.PlanNodeStatusFailed
			outcome.Err = execErr.Error()
		case result.UncertainSideEffect:
			outcome.Status = domain.PlanNodeStatusFailedPendingConfirmation
		default:
			outcome.Status = domain.PlanNodeStatusSucceeded
			outcome.Summary = result.Summary
		}
		s.PlanWaveOutcomes = append(s.PlanWaveOutcomes, outcome)
		return s, nil
	}
}

// planSlotChildState 隔离 plan 节点的子执行：禁用 plan 工具并清空波次
// 簿记，防止嵌套 plan 泄漏到子执行。
func planSlotChildState(s ReActState) ReActState {
	child := s
	child.ActivePlan = nil
	child.PlanToolsDisabled = true
	child.PlanWavePending = nil
	child.PlanWaveOutcomes = nil
	child.PlanContinueCallID = ""
	return child
}

// makePlanFinalizeNode 汇合一个 plan 波次：把记录的 outcomes 应用到
// ActivePlan（状态转换、attempts、revision、逐节点 checkpoint），随后用
// wave 观察恢复 LLM 上下文并清空波次簿记，使 LLM 节点恢复正常路由。
func makePlanFinalizeNode() NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.PlanWavePending) == 0 {
			return s, nil
		}
		s.PlanWavePending = nil
		// 先应用 outcomes 再构建观察文本：tool message 必须反映波次后的
		// plan 状态（含失败节点）。
		if _, err := applyPlanOutcomes(ctx, &s, s.PlanWaveOutcomes); err != nil {
			return s, err
		}
		s.PlanWaveOutcomes = nil
		if s.PlanContinueCallID != "" {
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:       "tool",
				Content:    planObservation("stratum_continue_plan", s.ActivePlan),
				ToolCallID: s.PlanContinueCallID,
			})
			s.PlanContinueCallID = ""
		}
		return s, nil
	}
}

// makeToolNext 是 tool 节点的条件边：存在已排程 plan 波次时进入注册槽位
// （每波次元素一个槽位），否则回到 LLM 节点。
func makeToolNext(s ReActState) []string {
	if len(s.PlanWavePending) > 0 {
		slots := make([]string, len(s.PlanWavePending))
		for i := range s.PlanWavePending {
			slots[i] = fmt.Sprintf("plan-%d", i)
		}
		return slots
	}
	return []string{nodeLLM}
}
