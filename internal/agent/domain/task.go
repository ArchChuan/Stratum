package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

var (
	// ErrGenerationConflict 表示 task 写回时 generation 不匹配（被另一会话
	// 接管后旧会话 stale 写），调用方应降级只读不重试。
	ErrGenerationConflict = errors.New("task generation conflict")
)

// TaskStatus 是 task 生命周期三态：active 可推进，completed 用户/LLM 明确完成
// （停更），abandoned 保留进度但不再推荐恢复（仅用户显式操作进入）。
type TaskStatus string

const (
	TaskStatusActive    TaskStatus = "active"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusAbandoned TaskStatus = "abandoned"
)

// Task 是"跨会话推进同一目标"的持久化实体。owner 是 (agent_id, user_id)，
// 允许同一 owner 多个活跃 task。并发防护三字段（claimed_by/lease_expires_at/
// generation）复用 workflow_runs 先例。last_conversation_id 是软引用，会话
// 删除只 detach 不级联。
type Task struct {
	ID                 string
	AgentID            string
	UserID             string
	Goal               string
	CurrentPhase       string
	CompletedSteps     []string
	NextAction         string
	Status             TaskStatus
	ClaimedBy          string
	LeaseExpiresAt     time.Time
	Generation         int64
	LastConversationID string
	LastExecutionID    string
	FailCount          int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          time.Time
}

// TaskSnapshot 是 Plan → Task 的一次提取结果（执行结束、挂点处生成）。
// JSON 用 camelCase 透出前端任务摘要条。Failures 是本次 execution 新增失败
// 节点数（挂点累加到 task.FailCount）。
type TaskSnapshot struct {
	Goal           string     `json:"goal"`
	CurrentPhase   string     `json:"currentPhase"`
	CompletedSteps []string   `json:"completedSteps"`
	NextAction     string     `json:"nextAction"`
	Status         TaskStatus `json:"status"`
	Failures       int        `json:"failures,omitempty"`
}

// BuildTaskSnapshot 从 Plan 映射 task 内容。Plan 无顶层 Goal，goal 取首个
// 节点；current_phase 从节点状态分布推导；next_action 取首个依赖满足的
// pending 节点。completeRequested（LLM 调 stratum_complete_task）或全部节点
// 达成或 plan 已 completed → status=completed。
func BuildTaskSnapshot(plan *Plan, completeRequested bool) TaskSnapshot {
	snapshot := TaskSnapshot{Status: TaskStatusActive}
	if plan == nil {
		return snapshot
	}
	completed := 0
	failed := 0
	var completedSteps []string
	for _, node := range plan.Nodes {
		switch node.Status {
		case PlanNodeStatusSucceeded:
			completed++
			completedSteps = append(completedSteps, node.ID)
		case PlanNodeStatusFailed, PlanNodeStatusFailedPendingConfirmation:
			failed++
		}
	}
	if len(plan.Nodes) > 0 {
		snapshot.Goal = plan.Nodes[0].Goal
		snapshot.CurrentPhase = fmt.Sprintf("%d/%d 完成", completed, len(plan.Nodes))
	}
	snapshot.CompletedSteps = completedSteps
	snapshot.Failures = failed
	snapshot.NextAction = nextActionOf(plan)
	if plan.Status == PlanStatusCompleted ||
		(len(plan.Nodes) > 0 && completed == len(plan.Nodes)) ||
		completeRequested {
		snapshot.Status = TaskStatusCompleted
	}
	return snapshot
}

// nextActionOf 返回首个 pending 且全部依赖已成功（无依赖即就绪）的节点目标；
// 无就绪节点返回空串（恢复时不注入）。
func nextActionOf(plan *Plan) string {
	byID := make(map[string]PlanNodeStatus, len(plan.Nodes))
	for _, node := range plan.Nodes {
		byID[node.ID] = node.Status
	}
	for _, node := range plan.Nodes {
		if node.Status != PlanNodeStatusPending {
			continue
		}
		ready := true
		for _, dep := range node.DependsOn {
			if byID[dep] != PlanNodeStatusSucceeded {
				ready = false
				break
			}
		}
		if ready {
			return node.Goal
		}
	}
	return ""
}

// ToTask 由新建路径组装持久化 Task：id 复用 plan.ID（一个 plan 一个稳定 task
// id），generation 从 0 起（新建无并发），lease/expiry 按常量设定。
func (s TaskSnapshot) ToTask(id, agentID, userID, conversationID, executionID string) Task {
	now := time.Now()
	return Task{
		ID: id, AgentID: agentID, UserID: userID,
		Goal: s.Goal, CurrentPhase: s.CurrentPhase, CompletedSteps: s.CompletedSteps,
		NextAction: s.NextAction, Status: s.Status,
		ClaimedBy: conversationID, LeaseExpiresAt: now.Add(constants.TaskLeaseDuration),
		LastConversationID: conversationID, LastExecutionID: executionID,
		FailCount: s.Failures, Generation: 0,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(constants.TaskExpiresAt),
	}
}
