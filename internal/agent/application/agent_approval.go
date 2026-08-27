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
	// 三段前置步骤（ApprovedPayload/TerminalResumePayload → 恢复层校验 → Registry.Get）
	// 提成 resumeContext，控制 ResumeToolApproval 复杂度并固化"D9 校验先于
	// Registry.Get"的顺序不变量。返回同一 executionID 的整批续跑条目（多审批），
	// terminal 语义逐条挂在 approvalResumeEntry.Terminal。
	entries, a, err := s.resumeContext(ctx, tenantID, actor, approvalID)
	if err != nil {
		return nil, 0, err
	}
	primary := entries[0].Payload
	// 抢占（B3）：AdvanceRunGeneration 分代 CAS + 状态 CAS 只放一个窗口胜出，杜绝
	// 按 approvalID 与 executionID 两路并发双 run 覆写 checkpoint。批量条目的归属
	// 校验已在 resolveApprovalResume/单条退路内完成（SECURITY-HIGH，含 cp 归属双
	// 保险），此处不再重复。checkpoint 已清（cp==nil）时不抢占，仍按批准载荷重跑。
	var cp *domain.AgentExecutionCheckpoint
	if s.deps.CheckpointStore != nil {
		cp, err = s.deps.CheckpointStore.GetLatest(ctx, tenantID, primary.ExecutionID)
		if err != nil {
			return nil, 0, fmt.Errorf("resume tool approval: get checkpoint: %w", err)
		}
	}
	if cp != nil {
		if err := s.claimApprovalResume(ctx, tenantID, primary.ExecutionID, cp); err != nil {
			return nil, 0, err
		}
	}
	req := ExecRequest{Query: primary.Query, ConversationID: primary.ConversationID, UserID: primary.UserID}
	meta := ExecMeta{TenantID: tenantID, TraceID: primary.TraceID,
		KnowledgeAssignmentsPinned: true, PinnedKnowledgeRevisions: primary.PinnedKnowledgeRevisions}
	_, options, err := s.assembleOptions(ctx, a, req, meta, primary.ExecutionID)
	if err != nil {
		return nil, 0, fmt.Errorf("resume tool approval: assemble options: %w", err)
	}
	// 复用 buildApprovalResumeOptions：恢复键（WithExecutionID + WithApprovalResume[s]）
	// + PinnedSkillRevisions 目录 + 覆盖式 guard（与批准一致的调用按调用键注入对应
	// ApprovalID 走 ExecuteApproved CAS 单次消费）。与流式续跑共用，消除重复；纯终态
	// 批次不追加 WithApprovalResume，guard 命中后 ExecuteApproved 返回友好错误让 LLM
	// 收尾。
	resumeOpts, consumed, err := s.buildApprovalResumeOptions(ctx, tenantID, a, entries)
	if err != nil {
		return nil, 0, err
	}
	options = append(options, resumeOpts...)
	start := time.Now()
	result, runErr := a.Execute(context.WithoutCancel(ctx), primary.Query, options...)
	// 纯终态批次跳过 approvedToolResumeError：终态不消费批准（consumed() 恒 false），
	// 套用会误报 ErrApprovedToolNotReplayed。收尾与 approved 一致——runErr==nil →
	// MarkCompleted、runErr!=nil → 原样返回（completeApprovalResume 对终态不做回滚，
	// 已决定行不可复活）。
	if !allTerminal(entries) {
		runErr = approvedToolResumeError(consumed(), runErr)
	}
	duration := int(time.Since(start).Milliseconds())
	runErr = completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, primary.ExecutionID, runErr)
	return result, duration, runErr
}

// resumeContext 组合 ResumeToolApproval 的三段前置步骤：ApprovedPayload/
// TerminalResumePayload → URL 审批恢复层校验（D9，先于 Registry.Get）→
// Registry.Get → 整批审批解析。返回同一 executionID 的续跑条目数组；任一审批仍
// pending 时 resolveApprovalResume 整体 202 上抛。not-found 折叠为 ErrNotFound。
// checkpoint 已清（entries==nil）时退化为 URL 单条续跑，D9 与归属校验在此补齐。

func (s *AgentService) resumeContext(ctx context.Context, tenantID, actor, approvalID string) ([]approvalResumeEntry, Agent, error) {
	payload, terminal, err := s.resumeApprovalPayload(ctx, tenantID, approvalID)
	if err != nil {
		return nil, nil, err
	}
	// D9 恢复层校验。终态审批跳过策略重查：工具不会执行（guard 终态拒绝），重查可能
	// 因 risk 差异 Invalidate 终态行——终态无转出，transition 校验失败反而阻断续跑；
	// 保留会话校验，避免向已删除会话写入。镜像流式 resolveTerminalApprovalResume。
	// 批量路径中每条（含 URL 审批）还会在 resolveApprovalResumeEntry 再次校验——
	// 校验只读 + Invalidate 幂等，重复执行无害，顺序不变量保持"D9 先于 Registry.Get"。
	if err := s.validateResumeApproval(ctx, tenantID, approvalID, payload, terminal); err != nil {
		return nil, nil, err
	}
	a, ok, err := s.deps.Registry.Get(ctx, payload.AgentID)
	if err != nil {
		return nil, nil, fmt.Errorf("resume tool approval: get agent: %w", err)
	}
	if !ok {
		return nil, nil, ErrNotFound
	}
	// 批量解析：从 checkpoint 取整批审批（含 URL approvalID），任一仍 pending → 202
	// 整体等待。批量条目已各自完成 authorize/binding/conversation/policy 校验。
	entries, _, err := s.resolveApprovalResume(ctx, tenantID, actor, payload.ExecutionID, payload.AgentID)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		// checkpoint 已清：按 URL 单条续跑。归属校验（SECURITY-HIGH）+ 恢复层校验
		// 在此补齐（镜像 resolveApprovalResumeEntry 的 approved 分支）。
		entries, err = s.fallbackURLResumeEntry(ctx, tenantID, actor, approvalID, payload, terminal)
		if err != nil {
			return nil, nil, err
		}
	}
	return entries, a, nil
}

// validateResumeApproval 审批续跑恢复层校验分发：终态审批跳过策略重查（工具不会
// 执行——guard 终态拒绝，重查可能因 risk 差异 Invalidate 终态行，终态无转出反而
// 阻断续跑），保留会话校验避免向已删除会话写入；非终态走完整 D9（会话+策略）。
func (s *AgentService) validateResumeApproval(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload, terminal bool) error {
	if terminal {
		return s.validateApprovalConversation(ctx, tenantID, approvalID, payload)
	}
	return s.validateApprovalResume(ctx, tenantID, approvalID, payload)
}

// fallbackURLResumeEntry checkpoint 已清（批量解析返回空）时按 URL 单条续跑：
// 补齐归属校验（SECURITY-HIGH）与恢复层校验，镜像 resolveApprovalResumeEntry 的
// approved 分支。返回单元素 entries。
func (s *AgentService) fallbackURLResumeEntry(
	ctx context.Context, tenantID, actor, approvalID string, payload ToolApprovalPayload, terminal bool,
) ([]approvalResumeEntry, error) {
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, nil); err != nil {
		return nil, err
	}
	if err := s.validateResumeApproval(ctx, tenantID, approvalID, payload, terminal); err != nil {
		return nil, err
	}
	return []approvalResumeEntry{{Payload: payload, ApprovalID: approvalID, Terminal: terminal}}, nil
}

// resumeApprovalPayload 解出可恢复审批载荷并统一失败处理（见 handleApprovedPayloadError）。
// approved 走原路径；终态审批（rejected/cancelled/expired）由 TerminalResumePayload 放行，
// 返回 terminal=true——非流式 ResumeToolApproval 与流式续跑语义一致。

func (s *AgentService) resumeApprovalPayload(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, bool, error) {
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err == nil {
		return payload, false, nil
	}
	terminalPayload, status, terr := s.deps.ApprovalService.TerminalResumePayload(ctx, tenantID, approvalID)
	if terr == nil && status != "" {
		return terminalPayload, true, nil
	}
	return ToolApprovalPayload{}, false, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
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
	// 只透出状态字符串，不含任何敏感字段。顶层单值镜像首条审批，兼容旧前端；
	// Approvals 数组携带同一轮全部审批，供多卡渲染与统一终态续跑判定。
	ApprovalStatus string                    `json:"approval_status,omitempty"`
	Approvals      []ActiveExecutionApproval `json:"approvals,omitempty"`
	UserQuery      string                    `json:"user_query,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

// ActiveExecutionApproval 是 waiting_approval checkpoint 中单条审批的刷新视图，
// 与顶层单值镜像同源（approval_ids 数组逐条解析），只透出恢复键与状态字符串。
type ActiveExecutionApproval struct {
	ApprovalID     string `json:"approval_id"`
	ApprovalStatus string `json:"approval_status,omitempty"`
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
		s.populateApprovalStatus(ctx, tenantID, approvalIDsFromRuntimeState(checkpoint.RuntimeStateJSON), out)
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

// populateApprovalStatus 读取 waiting_approval 全部审批行状态字符串供前端轮询，
// 只透出状态字符串、不泄露任何敏感字段。顶层单值镜像首条（兼容旧前端），
// Approvals 数组逐条填充同一轮全部审批（多卡渲染与统一终态判定）。读取失败仅
// 记录（fail-open）：前端保持等待态下轮重试，不影响 active-execution 本身
// （会话归属校验在调用方已完成）。

func (s *AgentService) populateApprovalStatus(ctx context.Context, tenantID string, approvalIDs []string, out *ActiveExecution) {
	if len(approvalIDs) == 0 {
		return
	}
	// ApprovalID 是恢复键的关联标识，ApprovalService 缺位时也照常透出；
	// 状态查询失败仅记录（fail-open）：前端保持等待态下轮重试。
	out.ApprovalID = approvalIDs[0]
	if s.deps.ApprovalService == nil {
		return
	}
	// 逐条查询：同一轮审批数量有限（均摊失败不影响整体）；单条失败仅记录，
	// 该条以空状态透出（前端保持等待态下轮重试），不因单条抖动拖垮整批。
	out.Approvals = make([]ActiveExecutionApproval, 0, len(approvalIDs))
	for _, approvalID := range approvalIDs {
		item := ActiveExecutionApproval{ApprovalID: approvalID}
		if st, err := s.deps.ApprovalService.ApprovalStatus(ctx, tenantID, approvalID); err == nil {
			item.ApprovalStatus = st
			if item.ApprovalID == out.ApprovalID {
				out.ApprovalStatus = st
			}
		} else {
			s.deps.Logger.Warn("agent: read approval status for active execution failed",
				zap.String("approval_id", approvalID),
				zap.Error(err))
		}
		out.Approvals = append(out.Approvals, item)
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

// approvalResumeEntry 是审批续跑的一条恢复条目：已批准（Terminal=false，工具可
// 执行）或终态（Terminal=true，rejected/cancelled/expired，工具不执行、LLM 收尾）。
// 一次整轮暂停产生的同一 executionID 的全部审批构成一个 batch，统一续跑。

type approvalResumeEntry struct {
	Payload    ToolApprovalPayload
	ApprovalID string
	Terminal   bool
}

// allTerminal 报告 batch 是否全部为终态条目（工具均不执行，LLM 直接收尾）。
// 供 ResumeToolApproval 决定是否跳过 approvedToolResumeError、buildApprovalResumeOptions
// 决定是否追加 WithApprovalResumes 恢复键、finishApprovalResume 决定终态收尾。

func allTerminal(entries []approvalResumeEntry) bool {
	for _, entry := range entries {
		if !entry.Terminal {
			return false
		}
	}
	return len(entries) > 0
}

// approvalIDsFromRuntimeState 提取 createApprovalCheckpoint 写入 waiting_approval
// checkpoint runtime state 的全部审批 ID：优先解析 approval_ids 数组（多审批），
// 回退旧格式单值 approval_id（兼容存量 checkpoint）。

func approvalIDsFromRuntimeState(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var state struct {
		ApprovalIDs []string `json:"approval_ids"`
		ApprovalID  string   `json:"approval_id"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil
	}
	if len(state.ApprovalIDs) > 0 {
		return state.ApprovalIDs
	}
	if state.ApprovalID != "" {
		return []string{state.ApprovalID}
	}
	return nil
}

// resolveApprovalResume 解析 executionID 对应的 waiting_approval 或 running
// checkpoint 并校验整批审批的续跑资格。返回续跑条目数组 + checkpoint；cp 为 nil
// 表示非审批续跑（无恢复键 / 无 checkpoint / 非 waiting_approval/running / 无
// 审批 ID）。任一审批仍 pending（ErrApprovalNotApproved）→ 整批 202 等待，不销毁
// 任何审批（transport 据此幂等恢复"等待审批"卡片）——满足"所有审批进入终态后才
// 触发继续执行会话"的统一续跑语义。
// SECURITY-HIGH：逐条非发起人（payload.UserID != actor）须为 admin/owner 现查
// 角色，否则 fail-closed 拒绝；审批过期 Invalidate、会话删除 Void、策略变更
// Invalidate 复用 validateApprovalResume 的恢复层校验；invalidated 错误原样上抛
// （409 授权撤销，非等待）。

func (s *AgentService) resolveApprovalResume(
	ctx context.Context, tenantID, actor, executionID, agentID string,
) ([]approvalResumeEntry, *domain.AgentExecutionCheckpoint, error) {
	cp, approvalIDs, err := s.approvalResumeCheckpoint(ctx, tenantID, executionID)
	if err != nil {
		return nil, nil, err
	}
	if cp == nil {
		return nil, nil, nil
	}
	entries := make([]approvalResumeEntry, 0, len(approvalIDs))
	for _, approvalID := range approvalIDs {
		entry, wait, err := s.resolveApprovalResumeEntry(ctx, tenantID, actor, approvalID, agentID, cp)
		if err != nil {
			return nil, nil, err
		}
		if wait {
			return nil, nil, ErrApprovalNotApproved
		}
		entries = append(entries, entry)
	}
	return entries, cp, nil
}

// resolveApprovalResumeEntry 解析单条审批的续跑资格：已批准 → 非终态条目（工具可
// 执行）；ApprovedPayload 失败 → 试终态放行（cancelled/rejected/expired → 终态
// 条目，主链路继续让 LLM 收尾）；仍 pending（ErrApprovalNotApproved）→ 返回
// wait=true 由整批 202 等待；invalidated → 原样上抛（409，授权撤销）；过期 →
// handleApprovedPayloadError（Invalidate(expired) 规范化后仍 410）。越权/会话
// 删除/binding mismatch 等校验错误原样上抛。

func (s *AgentService) resolveApprovalResumeEntry(
	ctx context.Context, tenantID, actor, approvalID, agentID string, cp *domain.AgentExecutionCheckpoint,
) (approvalResumeEntry, bool, error) {
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err == nil {
		if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
			return approvalResumeEntry{}, false, err
		}
		if err := s.validateApprovalBinding(agentID, payload); err != nil {
			return approvalResumeEntry{}, false, err
		}
		if err := s.validateApprovalResume(ctx, tenantID, approvalID, payload); err != nil {
			return approvalResumeEntry{}, false, err
		}
		return approvalResumeEntry{Payload: payload, ApprovalID: approvalID, Terminal: false}, false, nil
	}
	// 终态审批（已拒绝/已取消/已过期）：放行续跑，把"审批未通过"当作一次工具执行
	// 失败交给 LLM 收尾——工具不会执行（guard 对终态返回拒绝错误），主链路继续而非
	// 卡死。
	if terminalPayload, terminal, terr := s.resolveTerminalApprovalResume(ctx, tenantID, actor, approvalID, agentID, cp); terr != nil {
		return approvalResumeEntry{}, false, terr
	} else if terminal {
		return approvalResumeEntry{Payload: terminalPayload, ApprovalID: approvalID, Terminal: true}, false, nil
	}
	if errors.Is(err, ErrApprovalNotApproved) {
		// 仍 pending：整批 202 等待（统一续跑——所有审批进入终态才放行）。
		return approvalResumeEntry{}, true, nil
	}
	if errors.Is(err, domain.ErrApprovalInvalidated) {
		return approvalResumeEntry{}, false, err
	}
	return approvalResumeEntry{}, false, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
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
// checkpoint 并提取全部 approval_ids（数组解析，回退旧单值）。H2①：首个续跑抢占后
// checkpoint 转 running、但批准尚未消费时刷新，软续跑仍需命中注入批准载荷——
// running 亦视为审批续跑候选；正常 running 执行无 approval_ids
// （approvalIDsFromRuntimeState 为空）不受影响。非审批续跑（无恢复键 / 无
// checkpoint / 非 waiting_approval/running / 无审批 ID）返回 (nil, nil, nil)；
// DB 读取失败原样上抛。

func (s *AgentService) approvalResumeCheckpoint(
	ctx context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, []string, error) {
	if executionID == "" || s.deps.CheckpointStore == nil ||
		s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, nil, nil
	}
	cp, err := s.deps.CheckpointStore.GetLatest(ctx, tenantID, executionID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve approval resume: get checkpoint: %w", err)
	}
	if cp == nil || (cp.Status != domain.ExecStatusWaitingApproval && cp.Status != "running") {
		return nil, nil, nil
	}
	approvalIDs := approvalIDsFromRuntimeState(cp.RuntimeStateJSON)
	if len(approvalIDs) == 0 {
		return nil, nil, nil
	}
	return cp, approvalIDs, nil
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
// waiting_approval checkpoint 时抢占并把 req/meta 重写为首条批准载荷快照（query/
// 发起人/会话/知识 pin，同 executionID 的整批载荷共享同一发起人/会话），供
// assembleOptions 使用。调用方须在 assembleOptions 后追加
// buildApprovalResumeOptions，并在收尾调用 finishApprovalResume。任一审批仍
// pending 时整批 202 等待（resolveApprovalResume 上抛 ErrApprovalNotApproved）。
// 错误原样上抛（越权/过期/策略变/并发抢占失败）。
//
// H2① 软续跑：checkpoint 已为 running（首个续跑抢占后、批准消费前刷新）时不再
// 抢占（分代/状态 CAS 已由首个续跑完成，重复抢占会误报 409），仅注入批准载荷合成
// P1 直接执行；并发窗口由 ExecuteApproved 的 ClaimExecution CAS 保证单次消费。

func (s *AgentService) maybeResumeApproval(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, executionID string,
) (entries []approvalResumeEntry, resuming bool, outReq ExecRequest, outMeta ExecMeta, err error) {
	entries, cp, err := s.resolveApprovalResume(ctx, meta.TenantID, req.UserID, executionID, agentID)
	if err != nil {
		return nil, false, req, meta, err
	}
	if cp == nil {
		return nil, false, req, meta, nil
	}
	if cp.Status == domain.ExecStatusWaitingApproval {
		if err := s.claimApprovalResume(ctx, meta.TenantID, executionID, cp); err != nil {
			return nil, false, req, meta, err
		}
	}
	// 重跑以批准载荷为准：发起人/会话/query 必须是审批时快照，否则续跑会写到
	// 别的会话或以错误身份执行。整批条目同 executionID，共享同一发起人/会话/query。
	primary := entries[0].Payload
	req.Query = primary.Query
	req.UserID = primary.UserID
	req.ConversationID = primary.ConversationID
	meta.KnowledgeAssignmentsPinned = true
	meta.PinnedKnowledgeRevisions = primary.PinnedKnowledgeRevisions
	return entries, true, req, meta, nil
}

// buildApprovalResumeOptions 构造审批续跑的执行选项：恢复键（WithExecutionID +
// WithApprovalResumes[s]）与覆盖式 guard。guard 对与批准一致的调用
// （server/capability/canonical-digest 匹配）按调用键注入对应 approvalID 走
// ExecuteApproved 的 CAS 单次消费；后续不一致调用回退正常授权/审批路径。返回
// consumed 判定函数（"任一非终态条目被本轮 CAS 消费"），供收尾判定回滚条件——
// 部分消费后失败必须写 failed（回滚让 re-resume 撞上已执行条目 202 卡死），全部
// 未消费才允许回滚 waiting_approval。ResumeToolApproval 与流式续跑共用，消除重复。

func (s *AgentService) buildApprovalResumeOptions(
	ctx context.Context, tenantID string, a Agent, entries []approvalResumeEntry,
) ([]ExecutionOption, func() bool, error) {
	options, err := s.resolvePinnedSkillCatalog(ctx, tenantID, entries, make([]ExecutionOption, 0, 3))
	if err != nil {
		return nil, nil, err
	}
	// consumed[approvalID] 逐条记账：仅非终态条目可被 CAS 消费；终态条目
	// ExecuteApproved 必然失败不消费。byCallKey 按 server|capability|digest 命中
	// 注入对应 approvalID（同键多条取首个未消费，防 LLM 并行重复调用同工具），
	// byApprovalID 供 guard 闭包查终态标记。
	idx := indexApprovalEntries(entries)
	consumed := make(map[string]bool)
	primary := entries[0].Payload
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: s.deps.ToolAuthorizer,
		Executor:   s.deps.MCPToolExecutor,
		ExecuteApproved: func(callCtx context.Context, request ToolExecutionRequest) (port.MCPToolResult, error) {
			entry, ok := idx.byApprovalID[request.ApprovalID]
			return s.executeApprovedForResume(callCtx, tenantID, request.ApprovalID, request, ok && entry.Terminal, consumed)
		},
	})
	options = append(options, buildApprovalResumeKeyOptions(tenantID, a, entries, primary, guard, idx, consumed)...)
	return options, func() bool { return anyConsumed(entries, consumed) }, nil
}

// approvalResumeIndex 是审批续跑条目的调用键索引：byCallKey 按 server|capability|
// digest 命中注入对应 approvalID，byApprovalID 供 guard 闭包查终态标记。
type approvalResumeIndex struct {
	byCallKey    map[string][]approvalResumeEntry
	byApprovalID map[string]approvalResumeEntry
}

// indexApprovalEntries 构建审批续跑条目索引：canonical digest 比较（与
// ApprovedPayload binding 校验同源），容忍 int/float 表示差异；digest 解析失败
// 的条目不进入 byCallKey（无法命中注入，回退正常授权/审批路径）。
func indexApprovalEntries(entries []approvalResumeEntry) approvalResumeIndex {
	idx := approvalResumeIndex{
		byCallKey:    make(map[string][]approvalResumeEntry, len(entries)),
		byApprovalID: make(map[string]approvalResumeEntry, len(entries)),
	}
	for _, entry := range entries {
		idx.byApprovalID[entry.ApprovalID] = entry
		if d, dErr := CanonicalToolArgumentsDigest(entry.Payload.Arguments); dErr == nil {
			key := entry.Payload.ServerID + "|" + entry.Payload.ToolName + "|" + d
			idx.byCallKey[key] = append(idx.byCallKey[key], entry)
		}
	}
	return idx
}

// resolvePinnedSkillCatalog 合并同批审批共享的 pinned skill revisions（各载荷同
// 源）并解析目录：去重防脏数据导致重复目录解析，追加 WithSkillCatalog option。
// resolver 未配置或无 pinned 时原样返回 options。
func (s *AgentService) resolvePinnedSkillCatalog(
	ctx context.Context, tenantID string, entries []approvalResumeEntry, options []ExecutionOption,
) ([]ExecutionOption, error) {
	pinned := map[string]string{}
	for _, entry := range entries {
		for skillID, revisionID := range entry.Payload.PinnedSkillRevisions {
			pinned[skillID] = revisionID
		}
	}
	if len(pinned) == 0 || s.deps.SkillActivationResolver == nil {
		return options, nil
	}
	refs := make([]port.SkillRevisionRef, 0, len(pinned))
	for skillID, revisionID := range pinned {
		refs = append(refs, port.SkillRevisionRef{SkillID: skillID, RevisionID: revisionID})
	}
	catalog, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs)
	if err != nil {
		return nil, err
	}
	return append(options, WithSkillCatalog(catalog)), nil
}

// buildApprovalResumeKeyOptions 组装审批续跑恢复键 options：executionID 固定注入；
// 有非终态条目才追加 WithApprovalResumes + WithApprovalResumePayloads
// （finalizeReActCheckpoint 按普通执行收尾写终态，否则 resumeFromCheckpoint 走
// "恢复 running"路径导致重复 P1 注入）。纯终态批次只注入 executionID + 工具闭包。
func buildApprovalResumeKeyOptions(
	tenantID string, a Agent, entries []approvalResumeEntry, primary ToolApprovalPayload,
	guard *ToolExecutionGuard, idx approvalResumeIndex, consumed map[string]bool,
) []ExecutionOption {
	opts := []ExecutionOption{WithExecutionID(primary.ExecutionID)}
	if !allTerminal(entries) {
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.Terminal {
				ids = append(ids, entry.ApprovalID)
			}
		}
		opts = append(opts, WithApprovalResumes(ids))
	}
	payloads := make([]ToolApprovalPayload, 0, len(entries))
	for _, entry := range entries {
		payloads = append(payloads, entry.Payload)
	}
	return append(opts,
		// C2a：注入已批准/已终态载荷集合，executeReAct 据此合成一条 assistant 消息
		// 含 N 条 tool_call 直接进入工具节点，不再经 LLM 重新生成参数（修复审批续跑
		// 无限循环）。
		WithApprovalResumePayloads(payloads),
		WithToolExecutionFn(approvalResumeToolFn(tenantID, a, primary, guard, idx, consumed)),
	)
}

// approvalResumeToolFn 返回审批续跑的 WithToolExecutionFn 闭包：注入链路定位，按
// canonical digest 命中注入对应 approvalID（C2d，与 ApprovedPayload binding 校验
// 同源，容忍 int/float 表示差异；C2c 合成路径参数同引用平凡命中），已消费的批准
// 不再注入（LLM 后续同参数重调走正常审批路径），然后走 guard.Execute。payload.
// ToolName 是 capability id（与 request.Tool.CapabilityID 对应）。
func approvalResumeToolFn(
	tenantID string, a Agent, primary ToolApprovalPayload,
	guard *ToolExecutionGuard, idx approvalResumeIndex, consumed map[string]bool,
) func(callCtx context.Context, request port.ToolExecutionRequest) (any, error) {
	return func(callCtx context.Context, request port.ToolExecutionRequest) (any, error) {
		request.TenantID = tenantID
		request.UserID = primary.UserID
		request.AgentID = primary.AgentID
		request.TraceID = primary.TraceID
		request.ExecutionID = primary.ExecutionID
		request.AgentToolIDs = slices.Clone(a.GetConfig().MCPToolIDs)
		request.AgentToolIDs = append(request.AgentToolIDs, agentgraph.StratumDelegateToolName)
		if d, dErr := CanonicalToolArgumentsDigest(request.Arguments); dErr == nil {
			key := request.Tool.ServerID + "|" + request.Tool.CapabilityID + "|" + d
			for _, entry := range idx.byCallKey[key] {
				if !consumed[entry.ApprovalID] {
					request.ApprovalID = entry.ApprovalID
					break
				}
			}
		}
		return guard.Execute(callCtx, request)
	}
}

// anyConsumed 判定"任一非终态条目被本轮 CAS 消费"，供收尾判定回滚条件——部分消费
// 后失败必须写 failed（回滚让 re-resume 撞上已执行条目 202 卡死），全部未消费才
// 允许回滚 waiting_approval。
func anyConsumed(entries []approvalResumeEntry, consumed map[string]bool) bool {
	for _, entry := range entries {
		if !entry.Terminal && consumed[entry.ApprovalID] {
			return true
		}
	}
	return false
}

// executeApprovedForResume 审批续跑的 ExecuteApproved 封装：C2d 原子消费判定 +
// 终态模式友好文案。consumed[approvalID] 仅在决定被原子消费（ExecuteApproved 内部
// ClaimExecution CAS 成功）后置位；claim 失败（并发已消费/过期）与工具未发送/
// 必然失败（行已 ReleaseExecution 回滚 approved）都不算消费——收尾回滚
// waiting_approval，批准仍可再次消费。终态条目（已拒绝/已取消/已过期）
// ExecuteApproved 必然失败且不消费批准（显式清零防御，避免污染批量收尾判定），
// 错误包装为友好文案（%w 保留哨兵 + 行为约束），LLM 感知未执行后自行收尾。
// 非终态模式保持原样，不影响 C2 幂等/恢复卡片。

func (s *AgentService) executeApprovedForResume(
	callCtx context.Context, tenantID, approvalID string, request ToolExecutionRequest, terminal bool, consumed map[string]bool,
) (port.MCPToolResult, error) {
	result, err := s.deps.ApprovalService.ExecuteApproved(
		callCtx, tenantID, approvalID, request.Tool.ServerID,
		request.Tool.CapabilityID, request.Arguments, s.deps.MCPToolExecutor,
	)
	if terminal {
		consumed[approvalID] = false
		if err != nil {
			err = fmt.Errorf("%w：工具审批未通过（已被拒绝或取消），工具未执行，请勿重试该工具", err)
		}
		return result, err
	}
	switch {
	case err == nil:
		consumed[approvalID] = true
	case errors.Is(err, domain.ErrApprovalAlreadyExecuted):
		consumed[approvalID] = false
	default:
		var execErr *port.MCPToolExecutionError
		if errors.As(err, &execErr) &&
			(execErr.Outcome == port.ToolExecutionOutcomeNotSent || execErr.Outcome == port.ToolExecutionOutcomeDefiniteFailure) {
			consumed[approvalID] = false
		} else {
			consumed[approvalID] = true
		}
	}
	return result, err
}

// finishApprovalResume 审批续跑收尾（SECURITY-MEDIUM-2）：成功 → MarkCompleted；
// 审批等待/断线/并发已消费类错误保留 checkpoint 现状（断线可刷新恢复、新审批已把
// 状态写回 waiting_approval、ErrApprovalAlreadyExecuted 表示并发窗口已有胜者接管，
// 保留 running 交给胜者 MarkCompleted，不回滚不销毁）；真实失败且整批批准都未被
// 本轮消费（consumed() 为 false——任一非终态条目被 CAS 消费即为 true）→ 回滚
// waiting_approval，发起人可重试同一批批准；已消费（ExecuteApproved CAS 已把某
// 条 approved→executed）→ 写终态 failed 保留对账历史（部分消费后回滚会让
// re-resume 撞上已执行条目 202 卡死）。

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
