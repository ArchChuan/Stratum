// Tool approval + approval-resume machinery.

package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func (s *AgentService) ListPendingApprovals(ctx context.Context, tenantID, actorID string) ([]domain.ToolApproval, error) {
	if s.deps.ApprovalService == nil {
		return nil, errors.New("tool approval service not configured")
	}
	// F2：审批列表含 approved 待执行态（pending + approved）；配额计数仍 pending-only。
	return s.deps.ApprovalService.ListActionable(ctx, tenantID, actorID)
}

func (s *AgentService) DecideToolApproval(ctx context.Context, tenantID, id, decision, actor, reason string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.Decide(ctx, tenantID, id, decision, actor, reason)
}

// CancelToolApproval 取消待批审批：仅发起人本人（或 admin/owner 代撤）可取消，
// pending→cancelled。越权/不存在统一 ErrApprovalNotFound（关闭存在性 oracle，与
// ApprovalDetail 同构）；已决定/已过期返回 ErrApprovalAlreadyDecided/ErrApprovalExpired。

func (s *AgentService) CancelToolApproval(ctx context.Context, tenantID, actor, approvalID string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.CancelApproval(ctx, tenantID, approvalID, actor)
}

// ListApprovalHistory 分页查询租户审批历史（actor 用于内部角色现查，空值放行返回全部）。

func (s *AgentService) ListApprovalHistory(ctx context.Context, tenantID string, page, pageSize int, actor string) ([]domain.ToolApproval, int, error) {
	if s.deps.ApprovalService == nil {
		return nil, 0, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ListHistory(ctx, tenantID, page, pageSize, actor)
}

// ApprovalDetail 返回单个审批的脱敏详情（actor 用于内部角色现查）。

func (s *AgentService) ApprovalDetail(ctx context.Context, tenantID, id, actor string) (ApprovalDetail, error) {
	if s.deps.ApprovalService == nil {
		return ApprovalDetail{}, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ApprovalDetail(ctx, tenantID, id, actor)
}

// ExecuteApprovedAction 单次消费已批准审批并把动作交给执行器（D4/D5）。
// actor 现查角色：仅 admin/owner 可执行（fail closed）。

func (s *AgentService) ExecuteApprovedAction(ctx context.Context, tenantID, id, actor string, executor port.ApprovalActionExecutor) (map[string]any, error) {
	if s.deps.ApprovalService == nil {
		return nil, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ExecuteApprovedAction(ctx, tenantID, id, actor, executor)
}

// SetApprovalAssignee 指定审批人（actor 需具备 admin/owner 角色，内部现查）。

func (s *AgentService) SetApprovalAssignee(ctx context.Context, tenantID, id, assignee, actor string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.SetAssignee(ctx, tenantID, id, assignee, actor)
}

func (s *AgentService) ResumeToolApproval(ctx context.Context, tenantID, actor, approvalID string) (*AgentResult, int, error) {
	if s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, 0, errors.New("tool approval runtime not configured")
	}
	// 三段前置步骤（ApprovedPayload → 恢复层校验 → Registry.Get）提成 resumeContext，
	// 控制 ResumeToolApproval 复杂度并固化"D9 校验先于 Registry.Get"的顺序不变量。
	payload, a, err := s.resumeContext(ctx, tenantID, approvalID)
	if err != nil {
		return nil, 0, err
	}
	// 归属 + 抢占（SECURITY-HIGH + B3）：与 executionID 流式续跑共用同一栅栏——
	// 非发起人须 admin/owner 现查角色；AdvanceRunGeneration 分代 CAS + 状态 CAS
	// 只放一个窗口胜出，杜绝按 approvalID 与 executionID 两路并发双 run 覆写
	// checkpoint。checkpoint 已清（cp==nil）时不抢占、跳过归属双保险，仍按批准
	// 载荷重跑（ResumeToolApproval 不依赖 checkpoint 存在）。
	var cp *domain.AgentExecutionCheckpoint
	if s.deps.CheckpointStore != nil {
		cp, err = s.deps.CheckpointStore.GetLatest(ctx, tenantID, payload.ExecutionID)
		if err != nil {
			return nil, 0, fmt.Errorf("resume tool approval: get checkpoint: %w", err)
		}
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return nil, 0, err
	}
	if cp != nil {
		if err := s.claimApprovalResume(ctx, tenantID, payload.ExecutionID, cp); err != nil {
			return nil, 0, err
		}
	}
	req := ExecRequest{Query: payload.Query, ConversationID: payload.ConversationID, UserID: payload.UserID}
	meta := ExecMeta{TenantID: tenantID, TraceID: payload.TraceID,
		KnowledgeAssignmentsPinned: true, PinnedKnowledgeRevisions: payload.PinnedKnowledgeRevisions}
	_, options, err := s.assembleOptions(ctx, a, req, meta, payload.ExecutionID)
	if err != nil {
		return nil, 0, fmt.Errorf("resume tool approval: assemble options: %w", err)
	}
	// 复用 buildApprovalResumeOptions：恢复键（WithExecutionID + WithApprovalResume）
	// + PinnedSkillRevisions 目录 + 覆盖式 guard（首个与批准一致的调用走 ExecuteApproved
	// CAS 单次消费）。与流式续跑共用，消除重复。
	resumeOpts, consumed, err := s.buildApprovalResumeOptions(ctx, tenantID, a, payload, approvalID, false)
	if err != nil {
		return nil, 0, err
	}
	options = append(options, resumeOpts...)
	start := time.Now()
	result, runErr := a.Execute(context.WithoutCancel(ctx), payload.Query, options...)
	runErr = approvedToolResumeError(consumed(), runErr)
	duration := int(time.Since(start).Milliseconds())
	runErr = completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, payload.ExecutionID, runErr)
	return result, duration, runErr
}

// resumeContext 组合 ResumeToolApproval 的三段前置步骤：ApprovedPayload →
// 恢复层校验（D9，先于 Registry.Get）→ Registry.Get。not-found 折叠为
// ErrNotFound（与调用方原语义一致）。

func (s *AgentService) resumeContext(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, Agent, error) {
	payload, err := s.resumeApprovalPayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, nil, err
	}
	if err := s.validateApprovalResume(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, nil, err
	}
	a, ok, err := s.deps.Registry.Get(ctx, payload.AgentID)
	if err != nil {
		return ToolApprovalPayload{}, nil, fmt.Errorf("resume tool approval: get agent: %w", err)
	}
	if !ok {
		return ToolApprovalPayload{}, nil, ErrNotFound
	}
	return payload, a, nil
}

// resumeApprovalPayload 解出可恢复审批载荷并统一失败处理（见 handleApprovedPayloadError）。

func (s *AgentService) resumeApprovalPayload(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, error) {
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
	}
	return payload, nil
}

// validateApprovalResume 是断点恢复的恢复层校验（D9）：复用自己的旧授权前，重新
// 核验外部状态。任一校验失败都 fail closed——确认的状态变更终结审批（保留可对账
// 历史），瞬态读取失败拒绝恢复但不销毁审批（避免 DB 抖动永久作废有效授权并写入
// 假审计 reason）。拆成会话/策略两个单职责子校验以控制复杂度。

func (s *AgentService) validateApprovalResume(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	if err := s.validateApprovalConversation(ctx, tenantID, approvalID, payload); err != nil {
		return err
	}
	return s.validateApprovalPolicy(ctx, tenantID, approvalID, payload)
}

// handleApprovedPayloadError 统一 ApprovedPayload 失败处理：过期是不可逆终态，
// Invalidate(expired) 只是规范化 reason 标记——CAS 失败（已决定/已执行）忽略，
// 真实持久化失败 Join 暴露（不吞错）；其他错误原样传播。

func (s *AgentService) handleApprovedPayloadError(ctx context.Context, tenantID, approvalID string, err error) error {
	if !errors.Is(err, ErrApprovalExpired) {
		return err
	}
	if err := approvalTransitionErr(err, s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "expired")); err != nil {
		return err
	}
	return err
}

// validateApprovalConversation 会话存在性校验：会话确认不存在（ErrNotFound）才
// Void(conversation_deleted)；其他读取错误 fail closed 返回原始错误，不 Void。

func (s *AgentService) validateApprovalConversation(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	if payload.ConversationID == "" || s.deps.ChatStore == nil {
		return nil
	}
	if _, err := s.deps.ChatStore.GetConversation(ctx, tenantID, payload.ConversationID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("resume tool approval: check conversation: %w", err)
		}
		if err := approvalTransitionErr(err, s.deps.ApprovalService.Void(ctx, tenantID, approvalID, "conversation_deleted")); err != nil {
			return err
		}
		return domain.ErrApprovalConversationGone
	}
	return nil
}

// validateApprovalPolicy 策略重查：无法解析（resolver 错误或空风险，镜像
// resolveMCPToolRisk 的 unresolved 语义）→ fail closed 返回错误不 Invalidate；
// 已解析但等级不一致 → Invalidate(policy_changed)。

func (s *AgentService) validateApprovalPolicy(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	// 策略重查是 MCP-tool 语义：空值视为 mcp_tool（存量兼容），显式非 MCP 审批
	// （evaluation_action/mcp_policy/mcp_server）无 MCP tool risk——用无关的
	// server/tool 名重查可能解析出偶然不同的等级，误 Invalidate 有效审批并写误导
	// 审计 reason。门控镜像 createApprovalCheckpoint。
	if s.deps.MCPToolPolicy == nil || (payload.SubjectKind != "" && payload.SubjectKind != domain.SubjectKindMCPTool) {
		return nil
	}
	risk, riskErr := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, payload.ServerID, payload.ToolName)
	if riskErr != nil || risk == "" {
		if riskErr == nil {
			riskErr = errors.New("tool risk unresolved")
		}
		return fmt.Errorf("resume tool approval: resolve policy: %w", riskErr)
	}
	if risk != payload.RiskLevel {
		if err := approvalTransitionErr(fmt.Errorf("tool risk %q changed from %q", risk, payload.RiskLevel),
			s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "policy_changed")); err != nil {
			return err
		}
		return domain.ErrApprovalPolicyChanged
	}
	return nil
}

// approvalTransitionErr 统一恢复层终结动作的 CAS 语义：终态 CAS 失败
// （已执行/已决定，ErrApprovalAlreadyExecuted）按不可逆终态忽略返回 nil；
// 其他错误 Join 保留——恢复层任何终结动作失败都必须暴露，禁止吞错。

func approvalTransitionErr(cause, transitionErr error) error {
	if transitionErr == nil || errors.Is(transitionErr, domain.ErrApprovalAlreadyExecuted) {
		return nil
	}
	return errors.Join(cause, transitionErr)
}

func completeApprovalResume(
	ctx context.Context,
	checkpoints CheckpointStore,
	tenantID, executionID string,
	runErr error,
) error {
	if runErr != nil || checkpoints == nil {
		return runErr
	}
	if err := checkpoints.MarkCompleted(ctx, tenantID, executionID); err != nil {
		return fmt.Errorf("complete approved tool checkpoint: %w", err)
	}
	return nil
}

// ActiveExecution is the refresh-resume view of a conversation's in-flight
// execution, returned by GetActiveExecution for the frontend to reconnect a
// streamed run after a hard refresh (session continuity).

type ActiveExecution struct {
	ExecutionID string `json:"execution_id"`
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	ApprovalID  string `json:"approval_id,omitempty"`
	// ApprovalStatus 仅 waiting_approval 时填充审批行当前状态
	// （pending/approved/rejected/expired/...）：批准后 checkpoint 仍停在
	// waiting_approval，发起人前端需区分"已批准待续跑"与"仍等待"才能自动续跑；
	// 只透出状态字符串，不含任何敏感字段。
	ApprovalStatus string    `json:"approval_status,omitempty"`
	UserQuery      string    `json:"user_query,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GetActiveExecution returns the conversation's fresh in-flight execution for
// the actor, or (nil, nil) when none exists (404-none). Fail-closed ownership
// gate: a member must own the conversation; only admin/owner may read a
// conversation they do not own. Any DB read failure is returned as an error
// (→ 500) and is never folded into the 404-none sentinel — a transient read
// failure must not masquerade as "no active execution" and trigger a fresh
// run (duplicate execution / duplicate approval).

func (s *AgentService) GetActiveExecution(ctx context.Context, tenantID, conversationID, actor string) (*ActiveExecution, error) {
	if s.deps.ChatStore == nil || s.deps.CheckpointStore == nil {
		return nil, nil
	}
	allowed, err := s.resolveActiveExecutionAccess(ctx, tenantID, conversationID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, nil
	}
	checkpoint, err := s.deps.CheckpointStore.GetLatestActiveByConversation(ctx, tenantID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get active execution: get checkpoint: %w", err)
	}
	if checkpoint == nil {
		return nil, nil
	}
	out := &ActiveExecution{
		ExecutionID: checkpoint.ExecutionID,
		AgentID:     checkpoint.AgentID,
		Status:      checkpoint.Status,
		UserQuery:   checkpoint.UserQuery,
		UpdatedAt:   checkpoint.UpdatedAt,
	}
	if checkpoint.Status == domain.ExecStatusWaitingApproval {
		s.populateApprovalStatus(ctx, tenantID, approvalIDFromRuntimeState(checkpoint.RuntimeStateJSON), out)
	}
	return out, nil
}

// resolveActiveExecutionAccess 会话归属校验（fail-closed）：member 必须拥有会话；
// admin/owner 可读他人会话。会话不存在或非归属 member 一律返回不放行（404-none
// 哨兵），关闭存在性 oracle——非归属成员无法探测会话是否存在。角色现查（resolver
// 缺失/失败原样上抛，不折叠成 404，防 DB 抖动被误判为"无活跃执行"）。

func (s *AgentService) resolveActiveExecutionAccess(ctx context.Context, tenantID, conversationID, actor string) (bool, error) {
	conv, err := s.deps.ChatStore.GetConversation(ctx, tenantID, conversationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("get active execution: get conversation: %w", err)
	}
	if conv.UserID == actor {
		return true, nil
	}
	role, err := s.deps.TenantRoleResolver.ResolveTenantRole(ctx, tenantID, actor)
	if err != nil {
		return false, fmt.Errorf("get active execution: resolve role: %w", err)
	}
	return role == "admin" || role == "owner", nil
}

// populateApprovalStatus 读取 waiting_approval 审批行状态字符串供前端轮询，只透出
// 状态字符串、不泄露任何敏感字段。读取失败仅记录（fail-open）：前端保持等待态下轮
// 重试，不影响 active-execution 本身（会话归属校验在调用方已完成）。

func (s *AgentService) populateApprovalStatus(ctx context.Context, tenantID, approvalID string, out *ActiveExecution) {
	if approvalID == "" {
		return
	}
	// ApprovalID 是恢复键的关联标识，ApprovalService 缺位时也照常透出；
	// 状态查询失败仅记录（fail-open）：前端保持等待态下轮重试。
	out.ApprovalID = approvalID
	if s.deps.ApprovalService == nil {
		return
	}
	if st, err := s.deps.ApprovalService.ApprovalStatus(ctx, tenantID, approvalID); err == nil {
		out.ApprovalStatus = st
	} else {
		s.deps.Logger.Warn("agent: read approval status for active execution failed",
			zap.String("approval_id", approvalID),
			zap.Error(err))
	}
}

// ensureInitialCheckpoint records the "running(init)" checkpoint of a brand-new
// execution so the conversation has a discoverable in-flight state before the
// first per-step checkpoint is written. It stores the round's user_query
// (written once, retained by later Upsert ON CONFLICT) so a session whose run
// just started can be resumed verbatim after a refresh. Continuation entries
// (meta.ExecutionID != "") never touch the existing checkpoint. Init-checkpoint
// failure is logged and does not abort the run (fail-open, explicit): the row
// simply does not exist, so nothing can be mis-resumed.

func (s *AgentService) ensureInitialCheckpoint(ctx context.Context, meta ExecMeta, req ExecRequest, agentID, executionID string) {
	if meta.ExecutionID != "" || s.deps.CheckpointStore == nil {
		return
	}
	markCtx, markCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	defer markCancel()
	checkpoint := domain.AgentExecutionCheckpoint{
		ExecutionID:    executionID,
		TraceID:        meta.TraceID,
		ConversationID: req.ConversationID,
		AgentID:        agentID,
		UserID:         req.UserID,
		Status:         "running",
		ResumeReason:   "init",
		ExpiresAt:      time.Now().Add(constants.PlanCheckpointTTL),
		UserQuery:      req.Query,
		RunGeneration:  1,
	}
	if err := s.deps.CheckpointStore.Upsert(markCtx, meta.TenantID, checkpoint); err != nil {
		s.deps.Logger.Warn("agent: initial checkpoint failed",
			zap.String("execution_id", executionID),
			zap.Error(err))
	}
}

// approvalIDFromRuntimeState extracts the approval_id stored by
// createApprovalCheckpoint into a waiting_approval checkpoint's runtime state.

func approvalIDFromRuntimeState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var state struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	return state.ApprovalID
}

// resolveApprovalResume 解析 executionID 对应的 waiting_approval 或 running
// checkpoint 并校验审批续跑资格。返回 (payload, approvalID, checkpoint)；
// checkpoint 为 nil 表示非审批续跑（无恢复键 / 无 checkpoint / 非
// waiting_approval/running / 无审批 ID）。
// SECURITY-HIGH：非发起人（payload.UserID != actor）须为 admin/owner 现查角色，
// 否则 fail-closed 拒绝；审批过期 Invalidate、会话删除 Void、策略变更 Invalidate
// 复用 validateApprovalResume 的恢复层校验；未批准/已作废错误原样上抛（transport
// 据此幂等恢复"等待审批"卡片，不销毁审批）。

func (s *AgentService) resolveApprovalResume(
	ctx context.Context, tenantID, actor, executionID, agentID string,
) (ToolApprovalPayload, string, *domain.AgentExecutionCheckpoint, bool, error) {
	cp, approvalID, err := s.approvalResumeCheckpoint(ctx, tenantID, executionID)
	if err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if cp == nil {
		return ToolApprovalPayload{}, "", nil, false, nil
	}
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err != nil {
		// 终态审批（已拒绝/已取消）：放行续跑，把"审批未通过"当作一次工具执行失败
		// 交给 LLM 收尾——工具不会执行（guard 对终态返回拒绝错误），主链路继续而非卡死。
		return s.resolveApprovalResumeApprovedError(ctx, tenantID, actor, approvalID, agentID, cp, err)
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if err := s.validateApprovalBinding(agentID, payload); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if err := s.validateApprovalResume(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	return payload, approvalID, cp, false, nil
}

// resolveApprovalResumeApprovedError ApprovedPayload 失败分支：先试终态放行
// （cancelled/rejected → terminal=true，主链路继续），再按哨兵映射非终态错误
// （ErrApprovalNotApproved→202 等待、ErrApprovalExpired→410、invalidated→409）。
// 抽离使 resolveApprovalResume 主流程仅保留 checkpoint 定位与 approved 校验，
// 复杂度回到棘轮目标内。

func (s *AgentService) resolveApprovalResumeApprovedError(
	ctx context.Context, tenantID, actor, approvalID, agentID string, cp *domain.AgentExecutionCheckpoint, err error,
) (ToolApprovalPayload, string, *domain.AgentExecutionCheckpoint, bool, error) {
	if terminalPayload, terminal, terr := s.resolveTerminalApprovalResume(ctx, tenantID, actor, approvalID, agentID, cp); terr != nil {
		return ToolApprovalPayload{}, "", nil, false, terr
	} else if terminal {
		return terminalPayload, approvalID, cp, true, nil
	}
	if errors.Is(err, ErrApprovalNotApproved) || errors.Is(err, domain.ErrApprovalInvalidated) {
		return ToolApprovalPayload{}, approvalID, cp, false, err
	}
	return ToolApprovalPayload{}, "", nil, false, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
}

// resolveTerminalApprovalResume 终态审批（已拒绝/已取消）续跑判定。TerminalResumePayload
// 按 row.Status 显式枚举——仅 cancelled/rejected 放行，绝不放行 pending（误吞 pending
// 会绕过"审批未通过前必须等待"的门控）；终态行不做过期门控（已过 expires_at 的
// rejected/cancelled 仍放行，否则轮询到终态恰逢过期的竞态会断链）。非终态返回
// (payload, false, nil)，上层照旧走 ApprovedPayload 错误分支（ErrApprovalNotApproved
// →202 等待、ErrApprovalExpired→410、invalidated→409）。校验错误（越权/会话删除/
// binding mismatch）原样上抛。

func (s *AgentService) resolveTerminalApprovalResume(
	ctx context.Context, tenantID, actor, approvalID, agentID string, cp *domain.AgentExecutionCheckpoint,
) (ToolApprovalPayload, bool, error) {
	payload, _, err := s.deps.ApprovalService.TerminalResumePayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, false, nil
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	if err := s.validateApprovalBinding(agentID, payload); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	// 保留会话校验：终态续跑同样避免向已删除会话写入。
	if err := s.validateApprovalConversation(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	return payload, true, nil
}

// approvalResumeCheckpoint 定位 executionID 对应的 waiting_approval 或 running
// checkpoint 并提取 approval_id。H2①：首个续跑抢占后 checkpoint 转 running、但
// 批准尚未消费时刷新，软续跑仍需命中注入批准载荷——running 亦视为审批续跑候选；
// 正常 running 执行无 approval_id（approvalIDFromRuntimeState 为空）不受影响。
// 非审批续跑（无恢复键 / 无 checkpoint / 非 waiting_approval/running / 无审批
// ID）返回 (nil, "", nil)；DB 读取失败原样上抛。

func (s *AgentService) approvalResumeCheckpoint(
	ctx context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, string, error) {
	if executionID == "" || s.deps.CheckpointStore == nil ||
		s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, "", nil
	}
	cp, err := s.deps.CheckpointStore.GetLatest(ctx, tenantID, executionID)
	if err != nil {
		return nil, "", fmt.Errorf("resolve approval resume: get checkpoint: %w", err)
	}
	if cp == nil || (cp.Status != domain.ExecStatusWaitingApproval && cp.Status != "running") {
		return nil, "", nil
	}
	approvalID := approvalIDFromRuntimeState(cp.RuntimeStateJSON)
	if approvalID == "" {
		return nil, "", nil
	}
	return cp, approvalID, nil
}

// validateApprovalBinding URL 上的 agentID 必须与审批载荷 AgentID 一致，防止拿
// A 的审批续跑 B 的执行。

func (s *AgentService) validateApprovalBinding(agentID string, payload ToolApprovalPayload) error {
	// SECURITY-HIGH：URL 指定 agentID 时须与载荷 AgentID 一致；载荷缺 agent_id
	// 视为不匹配（fail-closed），防拿 A 的审批续跑 B 的执行——不再容忍双侧为空放行。
	if agentID != "" && agentID != payload.AgentID {
		return ErrApprovalBindingMismatch
	}
	return nil
}

// authorizeApprovalActor 审批续跑的归属校验（SECURITY-HIGH）：发起人放行且
// checkpoint 归属须与发起人一致（双保险）；非发起人须为 admin/owner（角色现查，
// resolver 缺失/失败 fail-closed，不读 JWT role claim）。

func (s *AgentService) authorizeApprovalActor(
	ctx context.Context, tenantID, actor string, payload ToolApprovalPayload, cp *domain.AgentExecutionCheckpoint,
) error {
	if payload.UserID == actor {
		// cp 为 nil（ResumeToolApproval 且 checkpoint 已清）时无 checkpoint 归属可
		// 校验，跳过该双保险；非 nil 时仍须一致（双保险保持）。
		if cp != nil && cp.UserID != "" && cp.UserID != actor {
			return domain.ErrApprovalRoleDenied
		}
		return nil
	}
	role, err := s.deps.TenantRoleResolver.ResolveTenantRole(ctx, tenantID, actor)
	if err != nil {
		return fmt.Errorf("resolve approval resume: resolve role: %w", err)
	}
	if role != "admin" && role != "owner" {
		return domain.ErrApprovalRoleDenied
	}
	return nil
}

// claimApprovalResume 抢占 waiting_approval checkpoint：先分代 CAS
// （AdvanceRunGeneration，双 tab/双设备只放一个胜出），再把状态 CAS 为 running
// （B3 + SECURITY-MEDIUM-2 的抢占）。任一失败 = 并发续跑已胜出，返回错误由
// transport 映射 409"已在其他窗口执行"。

func (s *AgentService) claimApprovalResume(ctx context.Context, tenantID, executionID string, cp *domain.AgentExecutionCheckpoint) error {
	if err := s.deps.CheckpointStore.AdvanceRunGeneration(ctx, tenantID, executionID, cp.RunGeneration); err != nil {
		return fmt.Errorf("resume approval: %w: another window already resumed execution", err)
	}
	if err := s.deps.CheckpointStore.UpdateStatusFrom(ctx, tenantID, executionID, domain.ExecStatusWaitingApproval, "running"); err != nil {
		return fmt.Errorf("resume approval: claim checkpoint: %w", err)
	}
	return nil
}

// maybeResumeApproval 是 Execute/ExecuteStream 的统一审批续跑入口：命中
// waiting_approval checkpoint 时抢占并把 req/meta 重写为批准载荷快照（query/
// 发起人/会话/知识 pin），供 assembleOptions 使用。调用方须在 assembleOptions
// 后追加 buildApprovalResumeOptions，并在收尾调用 finishApprovalResume。错误
// 原样上抛（越权/过期/策略变/并发抢占失败）。
//
// H2① 软续跑：checkpoint 已为 running（首个续跑抢占后、批准消费前刷新）时不再
// 抢占（分代/状态 CAS 已由首个续跑完成，重复抢占会误报 409），仅注入批准载荷合成
// P1 直接执行；并发窗口由 ExecuteApproved 的 ClaimExecution CAS 保证单次消费。

func (s *AgentService) maybeResumeApproval(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, executionID string,
) (payload ToolApprovalPayload, approvalID string, resuming bool, terminal bool, outReq ExecRequest, outMeta ExecMeta, err error) {
	payload, approvalID, cp, terminal, err := s.resolveApprovalResume(ctx, meta.TenantID, req.UserID, executionID, agentID)
	if err != nil {
		return payload, approvalID, false, false, req, meta, err
	}
	if cp == nil {
		return ToolApprovalPayload{}, "", false, false, req, meta, nil
	}
	if cp.Status == domain.ExecStatusWaitingApproval {
		if err := s.claimApprovalResume(ctx, meta.TenantID, executionID, cp); err != nil {
			return ToolApprovalPayload{}, "", false, false, req, meta, err
		}
	}
	// 重跑以批准载荷为准：发起人/会话/query 必须是审批时快照，否则续跑会写到
	// 别的会话或以错误身份执行。
	req.Query = payload.Query
	req.UserID = payload.UserID
	req.ConversationID = payload.ConversationID
	meta.KnowledgeAssignmentsPinned = true
	meta.PinnedKnowledgeRevisions = payload.PinnedKnowledgeRevisions
	return payload, approvalID, true, terminal, req, meta, nil
}

// buildApprovalResumeOptions 构造审批续跑的执行选项：恢复键（WithExecutionID +
// WithApprovalResume）与覆盖式 guard。guard 对首个与批准一致的调用
// （server/capability/arguments 匹配）注入 approvalID 走 ExecuteApproved 的
// CAS 单次消费；后续不一致调用回退正常授权/审批路径。返回 consumed 判定函数
// （本轮重跑是否真的消费了批准），供收尾判定回滚条件。ResumeToolApproval 与
// 流式续跑共用，消除重复。

func (s *AgentService) buildApprovalResumeOptions(
	ctx context.Context, tenantID string, a Agent, payload ToolApprovalPayload, approvalID string, terminal bool,
) ([]ExecutionOption, func() bool, error) {
	options := make([]ExecutionOption, 0, 3)
	if len(payload.PinnedSkillRevisions) > 0 && s.deps.SkillActivationResolver != nil {
		refs := make([]port.SkillRevisionRef, 0, len(payload.PinnedSkillRevisions))
		for skillID, revisionID := range payload.PinnedSkillRevisions {
			refs = append(refs, port.SkillRevisionRef{SkillID: skillID, RevisionID: revisionID})
		}
		catalog, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, WithSkillCatalog(catalog))
	}
	consumed := false
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: s.deps.ToolAuthorizer,
		Executor:   s.deps.MCPToolExecutor,
		ExecuteApproved: func(callCtx context.Context, request ToolExecutionRequest) (port.MCPToolResult, error) {
			return s.executeApprovedForResume(callCtx, tenantID, approvalID, request, terminal, &consumed)
		},
	})
	resumeKeyOpts := []ExecutionOption{WithExecutionID(payload.ExecutionID)}
	// 终态模式（已拒绝/已取消）不追加 WithApprovalResume：finalizeReActCheckpoint 按
	// 普通执行收尾写终态，否则 resumeFromCheckpoint 走"恢复 running"路径导致重复
	// P1 注入。approved 模式保留恢复键。
	if !terminal {
		resumeKeyOpts = append(resumeKeyOpts, WithApprovalResume(approvalID))
	}
	resumeKeyOpts = append(resumeKeyOpts,
		// C2a：注入已批准载荷，executeReAct 据此合成 P1 直接执行，不再经 LLM
		// 重新生成参数（修复审批续跑无限循环）。
		WithApprovalResumePayload(payload),
		WithToolExecutionFn(func(callCtx context.Context, request port.ToolExecutionRequest) (any, error) {
			request.TenantID = tenantID
			request.UserID = payload.UserID
			request.AgentID = payload.AgentID
			request.TraceID = payload.TraceID
			request.ExecutionID = payload.ExecutionID
			request.AgentToolIDs = slices.Clone(a.GetConfig().MCPToolIDs)
			request.AgentToolIDs = append(request.AgentToolIDs, agentgraph.StratumDelegateToolName)
			if !consumed && request.Tool.ServerID == payload.ServerID &&
				request.Tool.CapabilityID == payload.ToolName {
				// C2d 加固：canonical digest 比较（与 ApprovedPayload binding 校验同源），
				// 容忍 int/float 表示差异；C2c 合成路径参数同引用平凡命中。
				if d, dErr := CanonicalToolArgumentsDigest(request.Arguments); dErr == nil && d == payload.ArgumentsDigest {
					request.ApprovalID = approvalID
				}
			}
			return guard.Execute(callCtx, request)
		}))
	options = append(options, resumeKeyOpts...)
	return options, func() bool { return consumed }, nil
}

// executeApprovedForResume 审批续跑的 ExecuteApproved 封装：C2d 原子消费判定 +
// 终态模式友好文案。consumed 仅在决定被原子消费（ExecuteApproved 内部 ClaimExecution
// CAS 成功）后置位；claim 失败（并发已消费/过期）与工具未发送/必然失败（行已
// ReleaseExecution 回滚 approved）都不算消费——收尾回滚 waiting_approval，批准仍可
// 再次消费。终态模式（已拒绝/已取消）ExecuteApproved 必然返回 ErrApprovalNotApproved，
// 包装为友好文案（%w 保留哨兵 + 行为约束），LLM 感知后自行收尾。approved 模式保持
// 原样，不影响 C2 幂等/恢复卡片。

func (s *AgentService) executeApprovedForResume(
	callCtx context.Context, tenantID, approvalID string, request ToolExecutionRequest, terminal bool, consumed *bool,
) (port.MCPToolResult, error) {
	result, err := s.deps.ApprovalService.ExecuteApproved(
		callCtx, tenantID, approvalID, request.Tool.ServerID,
		request.Tool.CapabilityID, request.Arguments, s.deps.MCPToolExecutor,
	)
	switch {
	case err == nil:
		*consumed = true
	case errors.Is(err, domain.ErrApprovalAlreadyExecuted):
		*consumed = false
	default:
		var execErr *port.MCPToolExecutionError
		if errors.As(err, &execErr) &&
			(execErr.Outcome == port.ToolExecutionOutcomeNotSent || execErr.Outcome == port.ToolExecutionOutcomeDefiniteFailure) {
			*consumed = false
		} else {
			*consumed = true
		}
	}
	if terminal && err != nil {
		err = fmt.Errorf("%w：工具审批未通过（已被拒绝或取消），工具未执行，请勿重试该工具", err)
	}
	return result, err
}

// finishApprovalResume 审批续跑收尾（SECURITY-MEDIUM-2）：成功 → MarkCompleted；
// 审批等待/断线/并发已消费类错误保留 checkpoint 现状（断线可刷新恢复、新审批已把
// 状态写回 waiting_approval、ErrApprovalAlreadyExecuted 表示并发窗口已有胜者接管，
// 保留 running 交给胜者 MarkCompleted，不回滚不销毁）；真实失败且批准未被本轮消费
// → 回滚 waiting_approval，发起人可重试同一批准；已消费（ExecuteApproved CAS 已把
// approved→executed）→ 写终态 failed 保留对账历史。

func (s *AgentService) finishApprovalResume(ctx context.Context, tenantID, executionID string, consumed func() bool, terminal bool, runErr error) error {
	if s.deps.CheckpointStore == nil {
		return runErr
	}
	// 终态模式（已拒绝/已取消）：checkpoint 终态已由 finalizeReActCheckpoint 写入。
	// 收尾抽到 finishTerminalApprovalResume，见其注释（不回滚/不二次 Terminate）。
	if terminal {
		return finishTerminalApprovalResume(ctx, s.deps.CheckpointStore, tenantID, executionID, runErr)
	}
	if runErr == nil {
		return completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, executionID, nil)
	}
	if retainRunningError(runErr) || errors.Is(runErr, domain.ErrApprovalAlreadyExecuted) {
		return runErr
	}
	if consumed == nil || !consumed() {
		if rbErr := s.deps.CheckpointStore.UpdateStatusFrom(ctx, tenantID, executionID, "running", domain.ExecStatusWaitingApproval); rbErr != nil {
			return errors.Join(runErr, fmt.Errorf("rollback approval checkpoint: %w", rbErr))
		}
		return runErr
	}
	if termErr := s.deps.CheckpointStore.Terminate(ctx, tenantID, executionID, "failed"); termErr != nil {
		return errors.Join(runErr, fmt.Errorf("terminate approval checkpoint: %w", termErr))
	}
	return runErr
}

// finishTerminalApprovalResume 终态审批（已拒绝/已取消）续跑收尾：checkpoint 终态已由
// finalizeReActCheckpoint 写入（runErr==nil→MarkCompleted、runErr!=nil→Terminate failed）。
// runErr==nil 时 MarkCompleted 幂等（仅对 running 行生效，不查 RowsAffected）；runErr!=nil
// 直接 return——绝不再回滚 waiting_approval（否则前端轮询 cancelled→再续跑→再回滚，死
// 循环），也绝不二次 Terminate（finalizeReActCheckpoint 已写终态，双写报错）。

func finishTerminalApprovalResume(
	ctx context.Context, checkpoints CheckpointStore, tenantID, executionID string, runErr error,
) error {
	if runErr == nil {
		return completeApprovalResume(ctx, checkpoints, tenantID, executionID, nil)
	}
	return runErr
}

// PauseExecution marks a running execution's checkpoint as paused so it can be
// resumed later. No-op when the checkpoint store is not configured.
