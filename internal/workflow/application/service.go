package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/dag"
	"go.uber.org/zap"
)

type CreateDefinitionCommand struct {
	Name        string
	Description string
	Spec        domain.Spec
	InputSchema domain.InputSchema
}

type UpdateDefinitionCommand struct {
	Name             string
	Description      string
	Spec             domain.Spec
	InputSchema      domain.InputSchema
	ExpectedRevision int64
}

type DefinitionService struct {
	definitions  port.DefinitionRepository
	versions     port.VersionRepository
	newID        func() string
	failureAudit auditport.FailureAuditRecorder
	bindings     port.SkillBindingResolver
	logger       *zap.Logger
}

func NewDefinitionService(definitions port.DefinitionRepository, versions port.VersionRepository, newID func() string) *DefinitionService {
	return &DefinitionService{definitions: definitions, versions: versions, newID: newID, logger: zap.NewNop()}
}

// SetFailureAuditRecorder 注入失败资源操作审计。未注入时跳过记录。
func (s *DefinitionService) SetFailureAuditRecorder(r auditport.FailureAuditRecorder) {
	s.failureAudit = r
}

// SetSkillBindingResolver 注入 agent 技能绑定解析器，用于校验 skill 节点的
// agent-skill 引用关系。未注入时跳过绑定校验（测试/降级）。
func (s *DefinitionService) SetSkillBindingResolver(r port.SkillBindingResolver) {
	s.bindings = r
}

// validateSkillBindings 校验 spec 中所有 skill 节点引用的 agent 确实启用了该技能。
// resolver 存在但查询失败则传播错误（fail-closed），不允许静默放行。
func (s *DefinitionService) validateSkillBindings(ctx context.Context, tenantID string, spec domain.Spec) error {
	if s.bindings == nil {
		return nil
	}
	cache := make(map[string][]string, len(spec.Nodes))
	for _, node := range spec.Nodes {
		if node.Type != domain.NodeTypeSkill || node.AgentID == "" || node.SkillID == "" {
			continue
		}
		allowed, ok := cache[node.AgentID]
		if !ok {
			var err error
			allowed, err = s.bindings.AgentAllowedSkills(ctx, tenantID, node.AgentID)
			if err != nil {
				return err
			}
			cache[node.AgentID] = allowed
		}
		if err := domain.ValidateSkillBinding(allowed, node.SkillID); err != nil {
			return err
		}
	}
	return nil
}

// SetLogger 注入日志器（默认 Nop，测试与生产均可覆盖）。
func (s *DefinitionService) SetLogger(l *zap.Logger) {
	if l != nil {
		s.logger = l
	}
}
func (s *DefinitionService) Create(ctx context.Context, tenantID string, cmd CreateDefinitionCommand, actorID string) (*domain.Definition, error) {
	definition, err := domain.NewDefinition(s.newID(), cmd.Name, cmd.Description, cmd.Spec, normalizeInputSchema(cmd.InputSchema))
	if err != nil {
		return nil, err
	}
	// 草稿保存也强制图完整性（含环检测）：用户拖拽新边时前端已阻止成环，
	// 这里作为 fail-closed 兜底，避免非法拓扑流入存储。允许空图（画一半先保存）。
	if err := domain.ValidateSpecGraph(definition.Spec); err != nil {
		return nil, err
	}
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	ev, err := newWorkflowChangeAudit(definition.ID, auditdomain.ChangeOpCreate, actorID, nil, workflowSafeProjection(definition))
	if err != nil {
		return nil, err
	}
	if err := s.definitions.CreateDefinition(ctx, tenantID, definition, ev); err != nil {
		s.recordFailure(ctx, definition.ID, "create", err)
		return nil, err
	}
	return definition, nil
}

func (s *DefinitionService) Update(ctx context.Context, tenantID, id string, cmd UpdateDefinitionCommand, actorID string) (*domain.Definition, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	before := workflowSafeProjection(definition)
	if err := definition.UpdateDraft(cmd.Name, cmd.Description, cmd.Spec, cmd.ExpectedRevision, normalizeInputSchema(cmd.InputSchema)); err != nil {
		return nil, err
	}
	// 与 Create 一致：草稿更新强制图完整性（含环检测），fail-closed。
	if err := domain.ValidateSpecGraph(definition.Spec); err != nil {
		return nil, err
	}
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpUpdate, actorID, before, workflowSafeProjection(definition))
	if err != nil {
		return nil, err
	}
	if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision, ev); err != nil {
		s.recordFailure(ctx, id, "update", err)
		return nil, err
	}
	return definition, nil
}

func (s *DefinitionService) Delete(ctx context.Context, tenantID, id string, actorID string) error {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return err
	}
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpDelete, actorID, workflowSafeProjection(definition), nil)
	if err != nil {
		return err
	}
	return s.definitions.DeleteDefinition(ctx, tenantID, id, ev)
}

func normalizeInputSchema(schema domain.InputSchema) domain.InputSchema {
	if schema.TaskLabel == "" && schema.TaskDescription == "" && len(schema.Fields) == 0 {
		return domain.InputSchema{TaskLabel: "任务", Fields: []domain.InputField{}}
	}
	return schema
}

func (s *DefinitionService) Validate(ctx context.Context, tenantID, id string) error {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return err
	}
	return domain.ValidateSpec(definition.Spec)
}

func (s *DefinitionService) Get(ctx context.Context, tenantID, id string) (*domain.Definition, error) {
	return s.definitions.GetDefinition(ctx, tenantID, id)
}

func (s *DefinitionService) GetVersion(ctx context.Context, tenantID, id string) (*domain.Version, error) {
	return s.versions.GetVersion(ctx, tenantID, id)
}

func (s *DefinitionService) Publish(ctx context.Context, tenantID, id string, actorID string) (*domain.Version, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	// 发布前同样校验 skill 节点绑定关系：发布即对外生效，非法绑定必须 fail-closed。
	if err := s.validateSkillBindings(ctx, tenantID, definition.Spec); err != nil {
		return nil, err
	}
	projection := workflowSafeProjection(definition)
	ev, err := newWorkflowChangeAudit(id, auditdomain.ChangeOpPublish, actorID, projection, projection)
	if err != nil {
		return nil, err
	}
	if publisher, ok := s.versions.(port.AtomicVersionPublisher); ok {
		version, err := publisher.CreateNextVersion(ctx, tenantID, definition, s.newID(), ev)
		if err != nil {
			s.recordFailure(ctx, id, "publish", err)
			return nil, err
		}
		return version, nil
	}
	number, err := s.versions.NextVersionNumber(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	version, err := definition.Publish(s.newID(), number)
	if err != nil {
		return nil, err
	}
	if err := s.versions.CreateVersion(ctx, tenantID, version, ev); err != nil {
		s.recordFailure(ctx, id, "publish", err)
		return nil, err
	}
	return version, nil
}

// recordFailure 旁路记录一次失败的工作流创建/更新/发布（best-effort）。
// 记录失败仅 WARN，不改变主流程错误。
func (s *DefinitionService) recordFailure(ctx context.Context, id, op string, err error) {
	if s.failureAudit == nil {
		return
	}
	if recordErr := s.failureAudit.Record(ctx, auditport.ResourceFailure{
		ResourceKind: auditdomain.ResourceKindWorkflow,
		ResourceID:   id,
		Operation:    op,
		ErrorCode:    auditport.ClassifyFailure(err),
	}); recordErr != nil {
		s.logger.Warn("failed to record workflow failure audit",
			zap.String("definition_id", id),
			zap.String("op", op),
			zap.Error(recordErr))
	}
}

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
	executors port.NodeExecutorRegistry
	newID     func() string
	eventIDMu sync.Mutex
	logger    *zap.Logger
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
	output, traceID, err := r.agents.ExecuteAgent(ctx, request.TenantID, request.Node.AgentID, request.Input)
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
		if nodes[approval.NodeID].Type == domain.NodeTypeApproval {
			attempt.Status = domain.AttemptStatusSucceeded
			attempt.OutputSummary = `{"approved":true}`
		} else {
			attempt.Status = domain.AttemptStatusRetryWait
			attempt.RetryAt = nil
		}
		attempt.FenceToken, attempt.RunGeneration = run.Generation, run.Generation
		if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_started", "node started"); err != nil {
			return err
		}
	}
	return nil
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
	batch := &nodeBatch{
		ctx:        ctx,
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
	for index, node := range ready {
		if err := s.dispatchReadyNode(batch, index, node); err != nil {
			return err
		}
	}
	wg.Wait()
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
	result, execErr := s.executors.Execute(execCtx, port.NodeExecutionRequest{TenantID: batch.tenantID, RunID: batch.run.ID, Node: node, AttemptNo: attempt.AttemptNo, Input: attempt.Input, RunInput: cloneInput(batch.run.Input), NodeOutputs: outputMap(batch.states), IdempotencyKey: fmt.Sprintf("%s:%s:%d", batch.run.ID, node.ID, attempt.AttemptNo), Approved: approved, ApprovalID: approvalID, BeforeEffect: beforeEffect, OnOutputDelta: outputBuffer.Append})
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
func (s *RunService) commitOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	if outcome.err == nil && outcome.result.Paused {
		return s.commitPausedOutcome(ctx, tenantID, run, outcome)
	}
	if outcome.err != nil {
		return s.commitFailedOutcome(ctx, tenantID, run, outcome)
	}
	return s.commitSucceededOutcome(ctx, tenantID, run, outcome)
}

// commitPausedOutcome 持久化审批节点暂停：checkpoint 暂停状态并创建审批。
func (s *RunService) commitPausedOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
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

func commandHash(versionID string, input map[string]any) (string, error) {
	payload, err := json.Marshal(struct {
		VersionID string         `json:"version_id"`
		Input     map[string]any `json:"input"`
	}{VersionID: versionID, Input: input})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func latestAttempts(attempts []domain.NodeAttempt) map[string]domain.NodeAttempt {
	out := make(map[string]domain.NodeAttempt)
	for _, attempt := range attempts {
		if current, ok := out[attempt.NodeID]; !ok || attempt.AttemptNo > current.AttemptNo {
			out[attempt.NodeID] = attempt
		}
	}
	return out
}

func nextAttemptNo(attempts []domain.NodeAttempt, nodeID string) int {
	next := 1
	for _, attempt := range attempts {
		if attempt.NodeID == nodeID && attempt.AttemptNo >= next {
			next = attempt.AttemptNo + 1
		}
	}
	return next
}

func readySet(spec domain.Spec, states map[string]domain.NodeAttempt) (ready, skipped []domain.Node, complete bool, err error) {
	if !hasConditionalRouting(spec) {
		return readySetFromKernel(spec, states, time.Now())
	}
	incoming := make(map[string][]domain.Edge, len(spec.Nodes))
	byID := make(map[string]domain.Node, len(spec.Nodes))
	for _, node := range spec.Nodes {
		byID[node.ID] = node
	}
	for _, edge := range spec.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge)
	}
	terminal := 0
	for _, node := range spec.Nodes {
		state, exists := states[node.ID]
		if exists {
			switch state.Status {
			case domain.AttemptStatusSucceeded, domain.AttemptStatusSkipped:
				terminal++
				continue
			case domain.AttemptStatusRetryWait:
				if state.RetryAt == nil || !state.RetryAt.After(time.Now()) {
					ready = append(ready, node)
				}
				continue
			case domain.AttemptStatusFailed:
				return nil, nil, false, fmt.Errorf("node %s failed", node.ID)
			default:
				continue
			}
		}
		edges := incoming[node.ID]
		if len(edges) == 0 {
			ready = append(ready, node)
			continue
		}
		resolved, selected, selectedSucceeded, err := countIncomingEdgeResolutions(edges, states, byID, spec)
		if err != nil {
			return nil, nil, false, err
		}
		if resolved == len(edges) {
			if selected == 0 {
				skipped = append(skipped, node)
			} else if selectedSucceeded == selected {
				ready = append(ready, node)
			}
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].ID < skipped[j].ID })
	return ready, skipped, terminal == len(spec.Nodes), nil
}

func countIncomingEdgeResolutions(edges []domain.Edge, states map[string]domain.NodeAttempt, byID map[string]domain.Node, spec domain.Spec) (resolved, selected, selectedSucceeded int, err error) {
	for _, edge := range edges {
		source, ok := states[edge.From]
		if !ok {
			continue
		}
		switch source.Status {
		case domain.AttemptStatusSkipped:
			resolved++
		case domain.AttemptStatusSucceeded:
			resolved++
			chosen := true
			if byID[edge.From].Type == domain.NodeTypeCondition {
				value, parseErr := strconv.ParseBool(source.OutputSummary)
				if parseErr != nil {
					return 0, 0, 0, parseErr
				}
				chosen = conditionEdgeSelected(spec, edge.From, edge, value)
			}
			if chosen {
				selected++
				selectedSucceeded++
			}
		case domain.AttemptStatusFailed:
			return 0, 0, 0, fmt.Errorf("upstream node %s failed", edge.From)
		}
	}
	return resolved, selected, selectedSucceeded, nil
}

func hasConditionalRouting(spec domain.Spec) bool {
	for _, node := range spec.Nodes {
		if node.Type == domain.NodeTypeCondition {
			return true
		}
	}
	return false
}

func readySetFromKernel(
	spec domain.Spec,
	states map[string]domain.NodeAttempt,
	now time.Time,
) (ready, skipped []domain.Node, complete bool, err error) {
	dependencies := make(map[string][]string, len(spec.Nodes))
	for _, edge := range spec.Edges {
		dependencies[edge.To] = append(dependencies[edge.To], edge.From)
	}
	nodes := make([]dag.Node, 0, len(spec.Nodes))
	byID := make(map[string]domain.Node, len(spec.Nodes))
	statuses := make(map[string]dag.Status, len(states))
	for _, node := range spec.Nodes {
		nodes = append(nodes, dag.Node{ID: node.ID, DependsOn: dependencies[node.ID]})
		byID[node.ID] = node
		state, exists := states[node.ID]
		if !exists {
			continue
		}
		switch state.Status {
		case domain.AttemptStatusSucceeded, domain.AttemptStatusSkipped:
			statuses[node.ID] = dag.StatusSucceeded
		case domain.AttemptStatusFailed:
			return nil, nil, false, fmt.Errorf("node %s failed", node.ID)
		case domain.AttemptStatusRetryWait:
			if state.RetryAt != nil && state.RetryAt.After(now) {
				statuses[node.ID] = dag.StatusRunning
			}
		default:
			statuses[node.ID] = dag.StatusRunning
		}
	}
	readyIDs, _, complete, err := dag.Ready(dag.Snapshot{Nodes: nodes, Statuses: statuses})
	if err != nil {
		return nil, nil, false, err
	}
	ready = make([]domain.Node, 0, len(readyIDs))
	for _, id := range readyIDs {
		ready = append(ready, byID[id])
	}
	return ready, nil, complete, nil
}

func conditionEdgeSelected(spec domain.Spec, sourceID string, edge domain.Edge, value bool) bool {
	if edge.ConditionValue != nil {
		return *edge.ConditionValue == value
	}
	if !edge.Default {
		return false
	}
	for _, candidate := range spec.Edges {
		if candidate.From == sourceID && candidate.ConditionValue != nil && *candidate.ConditionValue == value {
			return false
		}
	}
	return true
}

func nodeInput(run *domain.Run, node domain.Node, states map[string]domain.NodeAttempt) (string, map[string]string, error) {
	incoming := make([]string, 0)
	for _, edge := range run.Snapshot.Edges {
		if edge.To == node.ID {
			if state, ok := states[edge.From]; ok && state.Status == domain.AttemptStatusSucceeded && edgeSelectedByState(run.Snapshot, edge, state) {
				incoming = append(incoming, edge.From)
			}
		}
	}
	if len(incoming) == 0 {
		data, err := json.Marshal(run.Input)
		return string(data), nil, err
	}
	if len(incoming) == 1 && len(node.InputMapping) == 0 {
		return states[incoming[0]].OutputSummary, nil, nil
	}
	inputs := map[string]any{"run_input": run.Input, "nodes": outputMap(states)}
	// missing 记录字段级引用解析失败的上游节点与原因：不阻断整个节点输入构造，
	// 而是返回给调用方触发上游重试（见 retryUpstreamOutput）。
	missing := make(map[string]string)
	if len(node.InputMapping) > 0 {
		mapped := make(map[string]any, len(node.InputMapping))
		for key, reference := range node.InputMapping {
			value, ok, missingUp, missingReason := resolveMappingReference(reference, run, states)
			if missingUp != "" {
				missing[missingUp] = missingReason
				continue
			}
			if ok {
				mapped[key] = value
			}
		}
		inputs = mapped
	}
	data, err := json.Marshal(inputs)
	return string(data), missing, err
}

// resolveMappingReference 解析单条 InputMapping 引用："input" 直传运行输入、
// nodes.<up>.output 透传上游整段输出、nodes.<up>.output.<key> 精确提取字段
// （提取失败返回 missingUp 由调用方触发上游重试），其余未知格式忽略不落映射。
func resolveMappingReference(reference string, run *domain.Run, states map[string]domain.NodeAttempt) (value any, ok bool, missingUp, missingReason string) {
	if reference == "input" {
		return run.Input, true, "", ""
	}
	parts := strings.Split(reference, ".")
	if len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "output" {
		if len(parts) >= 4 {
			// 精确字段引用 nodes.<up>.output.<key>：解析上游 JSON 提取字段。
			value, err := extractOutputField(states[parts[1]].OutputSummary, parts[3])
			if err != nil {
				return nil, false, parts[1], err.Error()
			}
			return value, true, "", ""
		}
		// 整段引用 nodes.<up>.output 保持历史行为：透传上游整段 OutputSummary。
		return states[parts[1]].OutputSummary, true, "", ""
	}
	return nil, false, "", ""
}

// retryUpstreamOutput 把上游输出契约违例（字段引用解析失败）落成对 agent/skill
// 节点的重试：构造 retryable 的 executionOutcome 并复用 commitRetryOrFail 的既有
// 重试通道（下一轮调度器会对过期 retry_wait 重新 ready）。上游不是可契约化的
// agent/skill 节点（作者引用了错误的节点输出）时直接返回错误使 run fail，
// 避免静默拼出错误数据。
func (s *RunService) retryUpstreamOutput(ctx context.Context, tenantID string, run *domain.Run, states map[string]domain.NodeAttempt, upstreamID, reason string) error {
	upstream, ok := nodeByID(run.Snapshot, upstreamID)
	if !ok {
		return fmt.Errorf("workflow contract retry: upstream node %q not found", upstreamID)
	}
	if upstream.Type != domain.NodeTypeAgent && upstream.Type != domain.NodeTypeSkill {
		return fmt.Errorf("workflow contract retry: node %q is not agent or skill, cannot be retried to satisfy output contract: %s", upstreamID, reason)
	}
	state, ok := states[upstreamID]
	if !ok {
		return fmt.Errorf("workflow contract retry: upstream node %q has no completed attempt", upstreamID)
	}
	retryNode := upstream
	if retryNode.Retry.MaxAttempts < contractRetryDefaultAttempts {
		// 内置重试预算仅在作者显式配置低于预算时生效（作者更高配置被尊重）。
		retryNode.Retry.MaxAttempts = contractRetryDefaultAttempts
	}
	outcome := executionOutcome{
		node:    retryNode,
		attempt: state,
		result:  port.NodeExecutionResult{Retryable: true, ErrorCode: "upstream_output_contract"},
		err:     fmt.Errorf("workflow upstream %q output violates contract: %s", upstreamID, reason),
	}
	return s.commitRetryOrFail(ctx, tenantID, outcome)
}

func edgeSelectedByState(spec domain.Spec, edge domain.Edge, state domain.NodeAttempt) bool {
	for _, node := range spec.Nodes {
		if node.ID == edge.From {
			if node.Type != domain.NodeTypeCondition {
				return true
			}
			value, _ := strconv.ParseBool(state.OutputSummary)
			return conditionEdgeSelected(spec, edge.From, edge, value)
		}
	}
	return false
}

func outputMap(states map[string]domain.NodeAttempt) map[string]string {
	out := make(map[string]string)
	for nodeID, state := range states {
		if state.Status == domain.AttemptStatusSucceeded {
			out[nodeID] = state.OutputSummary
		}
	}
	return out
}

func cloneInput(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func terminalOutput(spec domain.Spec, states map[string]domain.NodeAttempt) string {
	outgoing := make(map[string]bool)
	for _, edge := range spec.Edges {
		outgoing[edge.From] = true
	}
	ids := make([]string, 0)
	for _, node := range spec.Nodes {
		if !outgoing[node.ID] {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	outputs := make(map[string]string)
	for _, id := range ids {
		if state, ok := states[id]; ok && state.Status == domain.AttemptStatusSucceeded {
			outputs[id] = state.OutputSummary
		}
	}
	if len(outputs) == 1 {
		for _, output := range outputs {
			return output
		}
	}
	data, _ := json.Marshal(outputs)
	return string(data)
}
