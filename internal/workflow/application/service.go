package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

type StartRunCommand struct {
	VersionID      string
	Input          map[string]any
	IdempotencyKey string
	CreatedBy      string
}

type RunService struct {
	versions port.VersionRepository
	store    interface {
		port.RunRepository
		port.AttemptRepository
		port.EventRepository
	}
	executors      port.NodeExecutorRegistry
	agentApprovals port.AgentApprovalResolver
	newID          func() string
	eventIDMu      sync.Mutex
	logger         *zap.Logger
}

// SetAgentApprovalResolver 注入 agent 原生工具审批判定器，供 agent 节点暂停后的
// reconcile 判断审批是否已全部终态（可续跑）。由 wiring 装配时调用。
func (s *RunService) SetAgentApprovalResolver(resolver port.AgentApprovalResolver) {
	s.agentApprovals = resolver
}

func NewRunService(versions port.VersionRepository, store interface {
	port.RunRepository
	port.AttemptRepository
}, agents port.AgentExecutor, newID func() string) *RunService {
	return NewRunServiceWithRegistry(versions, eventCapableStore{RunRepository: store, AttemptRepository: store}, agentRegistry{agents: agents}, newID, zap.NewNop())
}

type eventCapableStore struct {
	port.RunRepository
	port.AttemptRepository
}

func (eventCapableStore) AppendEvent(_ context.Context, _ string, event domain.Event) (domain.Event, error) {
	return event, nil
}
func (eventCapableStore) ListEvents(context.Context, string, string, int64, int) ([]domain.Event, error) {
	return []domain.Event{}, nil
}

type agentRegistry struct{ agents port.AgentExecutor }

func (r agentRegistry) Execute(ctx context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	if request.Node.Type != domain.NodeTypeAgent {
		return port.NodeExecutionResult{}, fmt.Errorf("no executor for node type %s", request.Node.Type)
	}
	output, traceID, err := r.agents.ExecuteAgent(ctx, request.TenantID, request.Node.AgentID, request.UserID, request.ExecutionID, request.Input)
	if err != nil && errors.Is(err, port.ErrAgentApprovalPending) {
		// agent 原生工具审批待决：节点暂停等待审批，不进入失败重试。
		return port.NodeExecutionResult{Paused: true, ErrorCode: "agent_approval_required"}, nil
	}
	return port.NodeExecutionResult{Output: output, TraceID: traceID}, err
}

func NewRunServiceWithRegistry(versions port.VersionRepository, store interface {
	port.RunRepository
	port.AttemptRepository
	port.EventRepository
}, executors port.NodeExecutorRegistry, newID func() string, logger *zap.Logger) *RunService {
	return &RunService{versions: versions, store: store, executors: executors, newID: newID, logger: logger}
}

func (s *RunService) Start(ctx context.Context, tenantID string, cmd StartRunCommand) (*domain.Run, bool, error) {
	hash, err := commandHash(cmd.VersionID, cmd.Input)
	if err != nil {
		return nil, false, err
	}
	if _, atomic := s.store.(port.IdempotentRunCreator); !atomic {
		existing, err := s.store.FindRunByIdempotency(ctx, tenantID, cmd.IdempotencyKey)
		if err == nil {
			if existing.RequestHash != hash {
				return nil, false, domain.ErrIdempotencyConflict
			}
			return existing, false, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, false, err
		}
	}
	version, err := s.versions.GetVersion(ctx, tenantID, cmd.VersionID)
	if err != nil {
		return nil, false, err
	}
	run, err := domain.NewRun(s.newID(), version, cmd.Input, cmd.IdempotencyKey, hash)
	if err != nil {
		return nil, false, err
	}
	run.CreatedBy = cmd.CreatedBy
	if creator, ok := s.store.(port.IdempotentRunCreator); ok {
		return creator.CreateRunIdempotent(ctx, tenantID, run)
	}
	if err := s.store.CreateRun(ctx, tenantID, run); err != nil {
		return nil, false, err
	}
	return run, true, nil
}

func (s *RunService) StartAsync(ctx context.Context, tenantID string, cmd StartRunCommand) (*domain.Run, bool, error) {
	return s.Start(ctx, tenantID, cmd)
}

func (s *RunService) Execute(ctx context.Context, tenantID, runID string) error {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if handled, controlErr := s.handleBoundaryControl(ctx, tenantID, run); handled {
		return controlErr
	}
	switch run.Status {
	case domain.RunStatusQueued:
		if err := run.Start(); err != nil {
			return err
		}
		if err := s.checkpointRun(ctx, tenantID, run, "workflow.run_started", "workflow run started"); err != nil {
			return err
		}
	case domain.RunStatusRunning:
		eventType, summary := "workflow.run_started", "workflow run started"
		events, eventErr := s.store.ListEvents(ctx, tenantID, run.ID, 0, 1000)
		if eventErr != nil {
			return eventErr
		}
		for _, event := range events {
			if event.Type == "workflow.run_started" || event.Type == "workflow.run_recovered" {
				eventType, summary = "workflow.run_recovered", "workflow run recovered"
				break
			}
		}
		if err := s.checkpointRun(ctx, tenantID, run, eventType, summary); err != nil {
			return err
		}
	default:
		return domain.ErrInvalidTransition
	}
	if err := s.reconcileApprovalCheckpoints(ctx, tenantID, run); err != nil {
		return s.failRun(ctx, tenantID, run, err)
	}
	if handled, err := s.reconcileExpiredAttempts(ctx, tenantID, run); handled {
		return err
	}
	for {
		fresh, getErr := s.store.GetRun(ctx, tenantID, run.ID)
		if getErr != nil {
			return getErr
		}
		if handled, controlErr := s.handleBoundaryControl(ctx, tenantID, fresh); handled {
			return controlErr
		}
		run = fresh
		attempts, listErr := s.store.ListAttempts(ctx, tenantID, run.ID)
		if listErr != nil {
			return s.failRun(ctx, tenantID, run, listErr)
		}
		states := latestAttempts(attempts)
		ready, skipped, complete, readyErr := readySet(run.Snapshot, states)
		if readyErr != nil {
			return s.failRun(ctx, tenantID, run, readyErr)
		}
		for _, node := range skipped {
			attempt := domain.NodeAttempt{ID: s.newID(), RunID: run.ID, NodeID: node.ID, AttemptNo: nextAttemptNo(attempts, node.ID), Status: domain.AttemptStatusSkipped, EffectClass: node.EffectClass, FenceToken: run.Generation, RunGeneration: run.Generation}
			if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_skipped", "branch not selected"); err != nil {
				return s.failRun(ctx, tenantID, run, err)
			}
		}
		if len(skipped) > 0 {
			continue
		}
		if complete {
			output := terminalOutput(run.Snapshot, states)
			if output == "" {
				output = "{}"
			}
			if err := run.Complete(output); err != nil {
				return err
			}
			if err := s.checkpointRun(ctx, tenantID, run, "workflow.run_completed", "workflow run completed"); err != nil {
				return err
			}
			return nil
		}
		if len(ready) == 0 {
			if waitingForRetry(states) {
				run.Status = domain.RunStatusQueued
				if err := s.checkpointRun(ctx, tenantID, run, "workflow.run_retrying", "waiting for retry"); err != nil {
					return err
				}
				return nil
			}
			return s.failRun(ctx, tenantID, run, fmt.Errorf("workflow has no ready nodes"))
		}
		if err := s.executeReadyBatch(ctx, tenantID, run, attempts, states, ready); err != nil {
			return s.failRun(ctx, tenantID, run, err)
		}
		if run.Status == domain.RunStatusPaused || run.Status == domain.RunStatusManualIntervention {
			return nil
		}
	}
}

func (s *RunService) reconcileExpiredAttempts(ctx context.Context, tenantID string, run *domain.Run) (bool, error) {
	attempts, err := s.store.ListAttempts(ctx, tenantID, run.ID)
	if err != nil {
		return true, err
	}
	effects, hasEffects := s.store.(port.EffectRepository)
	var intents []domain.EffectIntent
	if hasEffects {
		intents, err = effects.ListEffectIntents(ctx, tenantID, run.ID)
		if err != nil {
			return true, err
		}
	}
	intentByAttempt := map[string]domain.EffectIntent{}
	for _, intent := range intents {
		intentByAttempt[intent.AttemptID] = intent
	}
	for _, attempt := range attempts {
		if attempt.Status != domain.AttemptStatusRunning || attempt.FenceToken >= run.Generation {
			continue
		}
		if attempt.EffectClass == domain.EffectClassNonIdempotent {
			if intent, ok := intentByAttempt[attempt.ID]; ok && intent.Status == domain.EffectIntentStatusStarted {
				if err := intent.MarkUnknown("worker lease expired after effect started", intent.RunGeneration); err != nil {
					return true, err
				}
				if err := effects.UpdateEffectIntent(ctx, tenantID, &intent, domain.EffectIntentStatusStarted); err != nil {
					return true, err
				}
			}
			attempt.Status, attempt.ErrorMessage, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusManualIntervention, "external effect result is unknown", run.Generation, run.Generation
			if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.manual_intervention", attempt.ErrorMessage); err != nil {
				return true, err
			}
			run.Status, run.ManualReason = domain.RunStatusManualIntervention, attempt.ErrorMessage
			if err := s.store.UpdateRun(ctx, tenantID, run); err != nil {
				return true, err
			}
			// UpdateRun 乐观锁成功即 generation+1,内存同步保证后续 CAS 基于最新值。
			run.Generation++
			return true, nil
		}
		attempt.Status, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusRetryWait, run.Generation, run.Generation
		attempt.RetryAt = nil
		if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_retrying", "recovered expired attempt"); err != nil {
			return true, err
		}
	}
	return false, nil
}

type runController interface {
	ControlRun(context.Context, string, string, int64, domain.RunStatus, string, domain.Event) error
}

func (s *RunService) handleBoundaryControl(ctx context.Context, tenantID string, run *domain.Run) (bool, error) {
	controller, ok := s.store.(runController)
	switch run.Status {
	case domain.RunStatusCancelRequested:
		if !ok {
			return true, fmt.Errorf("workflow control repository unavailable")
		}
		event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.canceled", Status: string(domain.RunStatusCanceled), OccurredAt: time.Now().UTC()}
		return true, controller.ControlRun(ctx, tenantID, run.ID, run.Generation, domain.RunStatusCanceled, run.CancelReason, event)
	case domain.RunStatusPauseRequested:
		if !ok {
			return true, fmt.Errorf("workflow control repository unavailable")
		}
		event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.paused", Status: string(domain.RunStatusPaused), OccurredAt: time.Now().UTC()}
		return true, controller.ControlRun(ctx, tenantID, run.ID, run.Generation, domain.RunStatusPaused, run.PauseReason, event)
	case domain.RunStatusPaused, domain.RunStatusManualIntervention, domain.RunStatusCanceled:
		return true, nil
	}
	return false, nil
}

func (s *RunService) reconcileApprovalCheckpoints(ctx context.Context, tenantID string, run *domain.Run) error {
	if err := s.reconcileAgentApprovalCheckpoints(ctx, tenantID, run); err != nil {
		return err
	}
	repository, ok := s.store.(port.ApprovalRepository)
	if !ok {
		return nil
	}
	approvals, err := repository.ListApprovals(ctx, tenantID, run.ID, false)
	if err != nil {
		return err
	}
	if len(approvals) == 0 {
		return nil
	}
	attempts, err := s.store.ListAttempts(ctx, tenantID, run.ID)
	if err != nil {
		return err
	}
	byID := map[string]domain.NodeAttempt{}
	for _, attempt := range attempts {
		byID[attempt.ID] = attempt
	}
	nodes := map[string]domain.Node{}
	for _, node := range run.Snapshot.Nodes {
		nodes[node.ID] = node
	}
	for _, approval := range approvals {
		if approval.Status == domain.ApprovalStatusRejected {
			return fmt.Errorf("approval %s rejected", approval.ID)
		}
		if approval.Status != domain.ApprovalStatusApproved {
			continue
		}
		attempt, exists := byID[approval.AttemptID]
		if !exists || attempt.Status != domain.AttemptStatusPaused {
			continue
		}
		if err := s.transitionApprovedAttempt(ctx, tenantID, run, approval, attempt, nodes); err != nil {
			return err
		}
	}
	return nil
}

// reconcileAgentApprovals 处理 agent 原生审批暂停（ErrorCode=="agent_approval_required"）
// 的 attempt：审批全部终态 → attempt 转 RetryWait（RetryAt=nil）立即重跑续跑；
// 仍 pending → RetryWait + RetryAt=未来轮询，主循环 seeing waitingForRetry 把 run
// 置 queued，下 tick 再判。run 状态收敛由主循环完成，这里只落 attempt 状态。
func (s *RunService) reconcileAgentApprovals(ctx context.Context, tenantID string, run *domain.Run) error {
	attempts, err := s.store.ListAttempts(ctx, tenantID, run.ID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Status != domain.AttemptStatusPaused || attempt.ErrorCode != "agent_approval_required" {
			continue
		}
		executionID := deterministicExecutionID(run.ID, attempt.NodeID)
		done, err := s.agentApprovals.ResolveAgentApproval(ctx, tenantID, executionID)
		if err != nil {
			return err
		}
		attempt.Status = domain.AttemptStatusRetryWait
		attempt.FenceToken, attempt.RunGeneration = run.Generation, run.Generation
		if done {
			attempt.RetryAt = nil
		} else {
			retryAt := time.Now().Add(constants.WorkflowPausePollInterval)
			attempt.RetryAt = &retryAt
		}
		if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_started", "node started"); err != nil {
			return err
		}
	}
	return nil
}

// reconcileAgentApprovalCheckpoints 注入 agent 审批 resolver 时推进 agent 原生
// 审批暂停 attempt 的状态；未注入时直接放行。
func (s *RunService) reconcileAgentApprovalCheckpoints(ctx context.Context, tenantID string, run *domain.Run) error {
	if s.agentApprovals == nil {
		return nil
	}
	return s.reconcileAgentApprovals(ctx, tenantID, run)
}

// transitionApprovedAttempt 将已批准审批对应的 attempt 置为续跑（非审批节点）
// 或完成（审批节点），并落一次 node_started checkpoint 事件。
func (s *RunService) transitionApprovedAttempt(ctx context.Context, tenantID string, run *domain.Run, approval domain.Approval, attempt domain.NodeAttempt, nodes map[string]domain.Node) error {
	if nodes[approval.NodeID].Type == domain.NodeTypeApproval {
		attempt.Status = domain.AttemptStatusSucceeded
		attempt.OutputSummary = `{"approved":true}`
	} else {
		attempt.Status = domain.AttemptStatusRetryWait
		attempt.RetryAt = nil
	}
	attempt.FenceToken, attempt.RunGeneration = run.Generation, run.Generation
	return s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_started", "node started")
}

func (s *RunService) approvedForNode(ctx context.Context, tenantID, runID, nodeID string) (bool, string) {
	approvals, ok := s.store.(port.ApprovalRepository)
	if !ok {
		return false, ""
	}
	rows, err := approvals.ListApprovals(ctx, tenantID, runID, false)
	if err != nil {
		return false, ""
	}
	for _, approval := range rows {
		if approval.NodeID == nodeID && approval.Status == domain.ApprovalStatusApproved {
			return true, approval.ID
		}
	}
	return false, ""
}

func waitingForRetry(states map[string]domain.NodeAttempt) bool {
	for _, state := range states {
		if state.Status == domain.AttemptStatusRetryWait {
			return true
		}
	}
	return false
}

type executionOutcome struct {
	node    domain.Node
	attempt domain.NodeAttempt
	result  port.NodeExecutionResult
	err     error
	effect  *domain.EffectIntent
	// approvalGeneration 是 approval 节点所在批的基准 generation,
	// 同批并行 approval 共享同一值,保证乐观锁目标一致
	approvalGeneration int64
}

type nodeOutputBuffer struct {
	mu      sync.Mutex
	append  func(string) error
	onError context.CancelFunc
	buffer  []rune
	err     error
	timer   *time.Timer
	closed  bool
}

func newNodeOutputBuffer(appendEvent func(string) error, onError context.CancelFunc) *nodeOutputBuffer {
	return &nodeOutputBuffer{append: appendEvent, onError: onError}
}

func (b *nodeOutputBuffer) Append(delta string) error {
	if delta == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.closed {
		return fmt.Errorf("workflow output buffer is closed")
	}
	b.buffer = append(b.buffer, []rune(delta)...)
	for len(b.buffer) >= constants.WorkflowOutputDeltaMaxRunes {
		if err := b.flushRunesLocked(constants.WorkflowOutputDeltaMaxRunes); err != nil {
			return err
		}
	}
	if len(b.buffer) > 0 && b.timer == nil {
		b.timer = time.AfterFunc(constants.WorkflowOutputFlushInterval, b.flushOnTimer)
	}
	return nil
}

func (b *nodeOutputBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if b.err != nil {
		return b.err
	}
	return b.flushRunesLocked(len(b.buffer))
}

func (b *nodeOutputBuffer) flushOnTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timer = nil
	if b.closed || b.err != nil || len(b.buffer) == 0 {
		return
	}
	_ = b.flushRunesLocked(len(b.buffer))
}

func (b *nodeOutputBuffer) flushRunesLocked(size int) error {
	if size <= 0 {
		return nil
	}
	text := string(b.buffer[:size])
	if err := b.append(text); err != nil {
		b.err = err
		b.onError()
		return err
	}
	b.buffer = b.buffer[size:]
	return nil
}

func (s *RunService) executeReadyBatch(ctx context.Context, tenantID string, run *domain.Run, attempts []domain.NodeAttempt, states map[string]domain.NodeAttempt, ready []domain.Node) error {
	limit := run.Snapshot.MaxConcurrency
	if limit <= 0 {
		limit = domain.MaxWorkflowConcurrency
	}
	if limit > len(ready) {
		limit = len(ready)
	}
	batchCtx, cancelBatch := context.WithCancel(ctx)
	batch := &nodeBatch{
		ctx:        batchCtx,
		tenantID:   tenantID,
		run:        run,
		attempts:   attempts,
		states:     states,
		refs:       referencedOutputKeys(run.Snapshot),
		sem:        make(chan struct{}, limit),
		generation: run.Generation,
		outcomes:   make([]executionOutcome, len(ready)),
		blocked:    make([]bool, len(ready)),
	}
	var wg sync.WaitGroup
	batch.wg = &wg
	var controlWg sync.WaitGroup
	controlWg.Add(1)
	go s.watchBatchControl(ctx, batchCtx, cancelBatch, tenantID, run.ID, &controlWg)
	for index, node := range ready {
		if err := s.dispatchReadyNode(batch, index, node); err != nil {
			cancelBatch()
			controlWg.Wait()
			return err
		}
	}
	wg.Wait()
	cancelBatch()
	controlWg.Wait()
	for index, outcome := range batch.outcomes {
		if batch.blocked[index] {
			continue
		}
		if err := s.commitOutcome(ctx, tenantID, run, outcome); err != nil {
			return err
		}
	}
	return nil
}

// watchBatchControl 轮询批所属 run 的边界控制状态：批内出现 pause_requested 或
// cancel_requested 时取消 batchCtx，中断运行中节点（agent 经 checkpoint 可恢复，
// 见 commitCanceledOutcome→commitPauseBoundary），使暂停/取消不必等节点自然结束。
// 轮询用 parent ctx（WithoutCancel）独立传播，避免 batch 取消后无法继续读库。
func (s *RunService) watchBatchControl(ctx context.Context, batchCtx context.Context, cancel context.CancelFunc, tenantID, runID string, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(constants.WorkflowPausePollInterval)
	defer ticker.Stop()
	pollCtx := context.WithoutCancel(ctx)
	for {
		select {
		case <-batchCtx.Done():
			return
		case <-ticker.C:
			fresh, err := s.store.GetRun(pollCtx, tenantID, runID)
			if err != nil {
				continue
			}
			if fresh.Status == domain.RunStatusPauseRequested || fresh.Status == domain.RunStatusCancelRequested {
				cancel()
				return
			}
		}
	}
}

// nodeBatch 承载 executeReadyBatch 内单批共享的执行状态：sem 限制并发、outcomes
// 收集结果、blocked 标记因上游输出契约违例而跳过提交的节点，供 dispatchReadyNode
// 与 runNodeExecution 引用。generation 冻结批起始 run.Generation，同批并行节点
// 共享同一期望 generation，避免第一个 approval 提交后内存 generation 漂移导致
// 后续 approval 乐观锁失败。
type nodeBatch struct {
	ctx        context.Context
	tenantID   string
	run        *domain.Run
	attempts   []domain.NodeAttempt
	states     map[string]domain.NodeAttempt
	refs       map[string][]string
	sem        chan struct{}
	generation int64
	outcomes   []executionOutcome
	blocked    []bool
	wg         *sync.WaitGroup
}

// dispatchReadyNode 准备单个 ready 节点：构造输入（含输出契约注入）、处理字段
// 引用解析失败的上游重试并阻塞本节点，approval 节点本批暂停待人工，其余提交到
// 并发池执行。
func (s *RunService) dispatchReadyNode(batch *nodeBatch, index int, node domain.Node) error {
	attemptNo := nextAttemptNo(batch.attempts, node.ID)
	input, missing, err := nodeInput(batch.run, node, batch.states)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		// 上游输出契约违例：本批跳过下游提交，触发上游重试，下批再调度。
		batch.blocked[index] = true
		for upstreamID, reason := range missing {
			if err := s.retryUpstreamOutput(batch.ctx, batch.tenantID, batch.run, batch.states, upstreamID, reason); err != nil {
				return err
			}
		}
		return nil
	}
	input = injectOutputContract(batch.refs, node, input)
	attempt := domain.NodeAttempt{ID: s.newID(), RunID: batch.run.ID, NodeID: node.ID, AttemptNo: attemptNo, Status: domain.AttemptStatusRunning, Input: input, EffectClass: node.EffectClass, FenceToken: batch.generation, RunGeneration: batch.generation}
	if err := s.checkpointAttempt(batch.ctx, batch.tenantID, attempt, "workflow.node_started", "node started"); err != nil {
		return err
	}
	if node.Type == domain.NodeTypeApproval {
		batch.outcomes[index] = executionOutcome{node: node, attempt: attempt, result: port.NodeExecutionResult{Paused: true, ErrorCode: "approval_required"}, approvalGeneration: batch.generation}
		return nil
	}
	batch.wg.Add(1)
	go s.runNodeExecution(batch, index, node, attempt)
	return nil
}

// runNodeExecution 在并发池中执行单个非 approval 节点：按节点超时构造执行上下文、
// 建立 effect fence、串流输出并写回 outcomes[index]。
func (s *RunService) runNodeExecution(batch *nodeBatch, index int, node domain.Node, attempt domain.NodeAttempt) {
	defer batch.wg.Done()
	batch.sem <- struct{}{}
	defer func() { <-batch.sem }()
	execCtx := batch.ctx
	cancelTimeout := func() {}
	if node.TimeoutMS > 0 {
		execCtx, cancelTimeout = context.WithTimeout(batch.ctx, time.Duration(node.TimeoutMS)*time.Millisecond)
	}
	defer cancelTimeout()
	execCtx, cancelExecution := context.WithCancel(execCtx)
	defer cancelExecution()
	approved, approvalID := s.approvedForNode(batch.ctx, batch.tenantID, batch.run.ID, node.ID)
	var fencedEffect *domain.EffectIntent
	beforeEffect := func() error {
		if node.Type != domain.NodeTypeMCPTool {
			return nil
		}
		effects, ok := s.store.(port.EffectFenceRepository)
		if !ok {
			return fmt.Errorf("workflow effect fence repository unavailable")
		}
		fencedEffect = domain.NewEffectIntent(s.newID(), batch.run.ID, node.ID, attempt.ID, batch.generation, node.EffectClass, fmt.Sprintf("%s:%s", batch.run.ID, node.ID))
		return effects.StartExternalEffect(execCtx, batch.tenantID, fencedEffect, batch.run.SchedulerOwner, batch.generation)
	}
	outputBuffer := newNodeOutputBuffer(func(text string) error {
		return s.appendNodeOutputDelta(execCtx, batch.tenantID, attempt, text)
	}, cancelExecution)
	result, execErr := s.executors.Execute(execCtx, port.NodeExecutionRequest{TenantID: batch.tenantID, RunID: batch.run.ID, Node: node, AttemptNo: attempt.AttemptNo, Input: attempt.Input, RunInput: cloneInput(batch.run.Input), NodeOutputs: outputMap(batch.states), IdempotencyKey: fmt.Sprintf("%s:%s:%d", batch.run.ID, node.ID, attempt.AttemptNo), Approved: approved, ApprovalID: approvalID, BeforeEffect: beforeEffect, OnOutputDelta: outputBuffer.Append, UserID: batch.run.CreatedBy, ExecutionID: deterministicExecutionID(batch.run.ID, node.ID)})
	if flushErr := outputBuffer.Close(); execErr == nil && flushErr != nil {
		execErr = fmt.Errorf("flush node output: %w", flushErr)
	}
	if execErr == nil {
		execErr = s.appendNodeToolSteps(execCtx, batch.tenantID, attempt, result.ToolSteps)
	}
	batch.outcomes[index] = executionOutcome{node: node, attempt: attempt, result: result, err: execErr, effect: fencedEffect}
}

func (s *RunService) appendNodeOutputDelta(
	ctx context.Context,
	tenantID string,
	attempt domain.NodeAttempt,
	text string,
) error {
	event := domain.Event{
		ID: s.nextDisplayEventID(), RunID: attempt.RunID, Type: "workflow.node_output_delta",
		NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, Payload: map[string]any{"text": text},
		OccurredAt: time.Now().UTC(),
	}
	_, err := s.store.AppendEvent(ctx, tenantID, event)
	return err
}

func (s *RunService) appendNodeToolSteps(
	ctx context.Context,
	tenantID string,
	attempt domain.NodeAttempt,
	steps []port.NodeToolStep,
) error {
	for _, step := range steps {
		event := domain.Event{
			ID: s.nextDisplayEventID(), RunID: attempt.RunID, Type: "workflow.node_tool_step",
			NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo,
			Payload: map[string]any{
				"tool_name":   truncateWorkflowText(step.ToolName, constants.WorkflowToolNameMaxRunes),
				"duration_ms": max(step.DurationMS, 0),
				"summary":     truncateWorkflowText(step.Summary, constants.WorkflowToolSummaryMaxRunes),
			},
			OccurredAt: time.Now().UTC(),
		}
		if _, err := s.store.AppendEvent(ctx, tenantID, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *RunService) nextDisplayEventID() string {
	s.eventIDMu.Lock()
	defer s.eventIDMu.Unlock()
	return s.newID()
}

func truncateWorkflowText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// commitOutcome 按执行结果分派：paused → 创建审批；失败 → 干预/取消/重试/
// 失败落库；成功 → effect 成功 + 完成 checkpoint。

func (s *RunService) Get(ctx context.Context, tenantID, runID string, actor Actor) (*domain.Run, []domain.NodeAttempt, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, nil, err
	}
	if err := authorizeRun(run, actor, RunActionRead); err != nil {
		return nil, nil, err
	}
	attempts, err := s.store.ListAttempts(ctx, tenantID, runID)
	return run, attempts, err
}

func (s *RunService) Events(
	ctx context.Context,
	tenantID, runID string,
	actor Actor,
	after int64,
	limit int,
) ([]domain.Event, error) {
	run, err := s.store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return nil, err
	}
	if err := authorizeRun(run, actor, RunActionEvents); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	return s.store.ListEvents(ctx, tenantID, runID, after, limit)
}

func (s *RunService) checkpointAttempt(ctx context.Context, tenantID string, attempt domain.NodeAttempt, eventType, summary string) error {
	event := domain.Event{ID: s.newID(), RunID: attempt.RunID, Type: eventType, Status: string(attempt.Status), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, Summary: summary, OccurredAt: time.Now().UTC()}
	if atomic, ok := s.store.(port.AtomicCheckpointRepository); ok {
		return atomic.CheckpointAttempt(ctx, tenantID, attempt, event)
	}
	if err := s.store.SaveAttempt(ctx, tenantID, attempt); err != nil {
		return err
	}
	_, err := s.store.AppendEvent(ctx, tenantID, event)
	return err
}

func (s *RunService) checkpointRun(ctx context.Context, tenantID string, run *domain.Run, eventType, summary string) error {
	event := domain.Event{ID: s.newID(), RunID: run.ID, Type: eventType, Status: string(run.Status), Summary: summary, OccurredAt: time.Now().UTC()}
	if atomic, ok := s.store.(port.AtomicCheckpointRepository); ok {
		return atomic.CheckpointRun(ctx, tenantID, run, event)
	}
	if err := s.store.UpdateRun(ctx, tenantID, run); err != nil {
		return err
	}
	// UpdateRun 乐观锁成功即 generation+1,内存同步保证 run 对象与库内 generation 一致。
	run.Generation++
	_, err := s.store.AppendEvent(ctx, tenantID, event)
	return err
}

func (s *RunService) failRun(ctx context.Context, tenantID string, run *domain.Run, cause error) error {
	if run.Status != domain.RunStatusRunning {
		return cause
	}
	if failErr := run.Fail(cause.Error()); failErr != nil {
		s.logger.Error("workflow.run_failed_transition",
			zap.String("tenant_id", tenantID), zap.String("run_id", run.ID),
			zap.String("cause", cause.Error()), zap.Error(failErr))
		return errors.Join(cause, fmt.Errorf("workflow: mark run %s failed: %w", run.ID, failErr))
	}
	// run.Fail 的状态推进在内存侧,失败状态必须持久化;写回失败必须显式暴露,
	// 禁止吞掉——否则 run 会停留在 running 直至租约过期才被重新回收。
	if persistErr := s.checkpointRun(ctx, tenantID, run, "workflow.run_failed", cause.Error()); persistErr != nil {
		s.logger.Error("workflow.run_failed_persist",
			zap.String("tenant_id", tenantID), zap.String("run_id", run.ID),
			zap.String("cause", cause.Error()), zap.Error(persistErr))
		return errors.Join(cause, fmt.Errorf("workflow: persist failed state of run %s: %w", run.ID, persistErr))
	}
	return cause
}
