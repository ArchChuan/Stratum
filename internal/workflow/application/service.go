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

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/dag"
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
	definitions port.DefinitionRepository
	versions    port.VersionRepository
	newID       func() string
}

func NewDefinitionService(definitions port.DefinitionRepository, versions port.VersionRepository, newID func() string) *DefinitionService {
	return &DefinitionService{definitions: definitions, versions: versions, newID: newID}
}

func (s *DefinitionService) Create(ctx context.Context, tenantID string, cmd CreateDefinitionCommand) (*domain.Definition, error) {
	definition, err := domain.NewDefinition(s.newID(), cmd.Name, cmd.Description, cmd.Spec, normalizeInputSchema(cmd.InputSchema))
	if err != nil {
		return nil, err
	}
	if err := s.definitions.CreateDefinition(ctx, tenantID, definition); err != nil {
		return nil, err
	}
	return definition, nil
}

func (s *DefinitionService) Update(ctx context.Context, tenantID, id string, cmd UpdateDefinitionCommand) (*domain.Definition, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if err := definition.UpdateDraft(cmd.Name, cmd.Description, cmd.Spec, cmd.ExpectedRevision, normalizeInputSchema(cmd.InputSchema)); err != nil {
		return nil, err
	}
	if err := s.definitions.UpdateDefinition(ctx, tenantID, definition, cmd.ExpectedRevision); err != nil {
		return nil, err
	}
	return definition, nil
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

func (s *DefinitionService) Publish(ctx context.Context, tenantID, id string) (*domain.Version, error) {
	definition, err := s.definitions.GetDefinition(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if publisher, ok := s.versions.(port.AtomicVersionPublisher); ok {
		return publisher.CreateNextVersion(ctx, tenantID, definition, s.newID())
	}
	number, err := s.versions.NextVersionNumber(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	version, err := definition.Publish(s.newID(), number)
	if err != nil {
		return nil, err
	}
	if err := s.versions.CreateVersion(ctx, tenantID, version); err != nil {
		return nil, err
	}
	return version, nil
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
}

func NewRunService(versions port.VersionRepository, store interface {
	port.RunRepository
	port.AttemptRepository
}, agents port.AgentExecutor, newID func() string) *RunService {
	return NewRunServiceWithRegistry(versions, eventCapableStore{RunRepository: store, AttemptRepository: store}, agentRegistry{agents: agents}, newID)
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
}, executors port.NodeExecutorRegistry, newID func() string) *RunService {
	return &RunService{versions: versions, store: store, executors: executors, newID: newID}
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
	sem := make(chan struct{}, limit)
	outcomes := make([]executionOutcome, len(ready))
	var wg sync.WaitGroup
	for index, node := range ready {
		attemptNo := nextAttemptNo(attempts, node.ID)
		input, err := nodeInput(run, node, states)
		if err != nil {
			return err
		}
		attempt := domain.NodeAttempt{ID: s.newID(), RunID: run.ID, NodeID: node.ID, AttemptNo: attemptNo, Status: domain.AttemptStatusRunning, Input: input, EffectClass: node.EffectClass, FenceToken: run.Generation, RunGeneration: run.Generation}
		if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.node_started", "node started"); err != nil {
			return err
		}
		if node.Type == domain.NodeTypeApproval {
			outcomes[index] = executionOutcome{node: node, attempt: attempt, result: port.NodeExecutionResult{Paused: true, ErrorCode: "approval_required"}}
			continue
		}
		wg.Add(1)
		go func(index int, node domain.Node, attempt domain.NodeAttempt, effect *domain.EffectIntent) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			execCtx := ctx
			cancelTimeout := func() {}
			if node.TimeoutMS > 0 {
				execCtx, cancelTimeout = context.WithTimeout(ctx, time.Duration(node.TimeoutMS)*time.Millisecond)
			}
			defer cancelTimeout()
			execCtx, cancelExecution := context.WithCancel(execCtx)
			defer cancelExecution()
			approved, approvalID := s.approvedForNode(ctx, tenantID, run.ID, node.ID)
			var fencedEffect *domain.EffectIntent
			beforeEffect := func() error {
				if node.Type != domain.NodeTypeMCPTool {
					return nil
				}
				effects, ok := s.store.(port.EffectFenceRepository)
				if !ok {
					return fmt.Errorf("workflow effect fence repository unavailable")
				}
				fencedEffect = domain.NewEffectIntent(s.newID(), run.ID, node.ID, attempt.ID, run.Generation, node.EffectClass, fmt.Sprintf("%s:%s", run.ID, node.ID))
				return effects.StartExternalEffect(execCtx, tenantID, fencedEffect, run.SchedulerOwner, run.Generation)
			}
			outputBuffer := newNodeOutputBuffer(func(text string) error {
				return s.appendNodeOutputDelta(execCtx, tenantID, attempt, text)
			}, cancelExecution)
			result, execErr := s.executors.Execute(execCtx, port.NodeExecutionRequest{TenantID: tenantID, RunID: run.ID, Node: node, AttemptNo: attempt.AttemptNo, Input: attempt.Input, RunInput: cloneInput(run.Input), NodeOutputs: outputMap(states), IdempotencyKey: fmt.Sprintf("%s:%s:%d", run.ID, node.ID, attempt.AttemptNo), Approved: approved, ApprovalID: approvalID, BeforeEffect: beforeEffect, OnOutputDelta: outputBuffer.Append})
			if flushErr := outputBuffer.Close(); execErr == nil && flushErr != nil {
				execErr = fmt.Errorf("flush node output: %w", flushErr)
			}
			if execErr == nil {
				execErr = s.appendNodeToolSteps(execCtx, tenantID, attempt, result.ToolSteps)
			}
			outcomes[index] = executionOutcome{node: node, attempt: attempt, result: result, err: execErr, effect: fencedEffect}
		}(index, node, attempt, nil)
	}
	wg.Wait()
	for _, outcome := range outcomes {
		if err := s.commitOutcome(ctx, tenantID, run, outcome); err != nil {
			return err
		}
	}
	return nil
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

func (s *RunService) commitOutcome(ctx context.Context, tenantID string, run *domain.Run, outcome executionOutcome) error {
	attempt := outcome.attempt
	if outcome.err == nil && outcome.result.Paused {
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
		approval := domain.NewApproval(s.newID(), run.ID, attempt.NodeID, attempt.ID, run.Generation+1, reason, risk, attempt.Input)
		if err := approvals.CreateApproval(ctx, tenantID, approval, domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.approval_requested", NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, Status: string(domain.ApprovalStatusPending), Summary: reason, OccurredAt: time.Now().UTC()}); err != nil {
			return err
		}
		run.Status, run.PauseReason, run.Generation = domain.RunStatusPaused, reason, approval.RunGeneration
		return nil
	}
	if outcome.err != nil {
		if outcome.effect != nil && outcome.effect.Status == domain.EffectIntentStatusStarted && outcome.effect.EffectClass == domain.EffectClassNonIdempotent {
			effects := s.store.(port.EffectRepository)
			if err := outcome.effect.MarkUnknown(outcome.err.Error(), run.Generation); err != nil {
				return err
			}
			if err := effects.UpdateEffectIntent(ctx, tenantID, outcome.effect, domain.EffectIntentStatusStarted); err != nil {
				return err
			}
			attempt.Status, attempt.ErrorMessage = domain.AttemptStatusManualIntervention, outcome.err.Error()
			if err := s.checkpointAttempt(ctx, tenantID, attempt, "workflow.manual_intervention", run.ManualReason); err != nil {
				return err
			}
			run.Status, run.ManualReason = domain.RunStatusManualIntervention, "external effect result is unknown"
			run.Generation++
			if err := s.store.UpdateRun(ctx, tenantID, run); err != nil {
				return err
			}
			return nil
		}
		if outcome.effect != nil && outcome.effect.Status == domain.EffectIntentStatusStarted {
			effects := s.store.(port.EffectRepository)
			outcome.effect.Status, outcome.effect.Reason = domain.EffectIntentStatusFailed, outcome.err.Error()
			if err := effects.UpdateEffectIntent(context.WithoutCancel(ctx), tenantID, outcome.effect, domain.EffectIntentStatusStarted); err != nil {
				return err
			}
		}
		if errors.Is(outcome.err, context.Canceled) {
			fresh, err := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
			if err != nil {
				return err
			}
			if fresh.Status == domain.RunStatusPauseRequested {
				attempt.Status, attempt.ErrorMessage, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusRetryWait, "paused at node boundary", fresh.Generation, fresh.Generation
				attempt.RetryAt = nil
				if err := s.checkpointAttempt(context.WithoutCancel(ctx), tenantID, attempt, "workflow.node_paused", attempt.ErrorMessage); err != nil {
					return err
				}
				controller, ok := s.store.(runController)
				if !ok {
					return fmt.Errorf("workflow control repository unavailable")
				}
				event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.paused", Status: string(domain.RunStatusPaused), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, OccurredAt: time.Now().UTC()}
				if err := controller.ControlRun(context.WithoutCancel(ctx), tenantID, run.ID, fresh.Generation, domain.RunStatusPaused, fresh.PauseReason, event); err != nil {
					return err
				}
				latest, getErr := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
				if getErr == nil {
					*run = *latest
				}
				return nil
			}
		}
		if errors.Is(outcome.err, context.Canceled) {
			fresh, err := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
			if err != nil {
				return err
			}
			if fresh.Status == domain.RunStatusCancelRequested {
				attempt.Status, attempt.ErrorMessage, attempt.FenceToken, attempt.RunGeneration = domain.AttemptStatusCanceled, "canceled", fresh.Generation, fresh.Generation
				if err := s.checkpointAttempt(context.WithoutCancel(ctx), tenantID, attempt, "workflow.node_canceled", "canceled"); err != nil {
					return err
				}
				controller, ok := s.store.(runController)
				if !ok {
					return fmt.Errorf("workflow control repository unavailable")
				}
				event := domain.Event{ID: s.newID(), RunID: run.ID, Type: "workflow.canceled", Status: string(domain.RunStatusCanceled), NodeID: attempt.NodeID, AttemptNo: attempt.AttemptNo, OccurredAt: time.Now().UTC()}
				if err := controller.ControlRun(context.WithoutCancel(ctx), tenantID, run.ID, fresh.Generation, domain.RunStatusCanceled, fresh.CancelReason, event); err != nil {
					return err
				}
				latest, getErr := s.store.GetRun(context.WithoutCancel(ctx), tenantID, run.ID)
				if getErr == nil {
					*run = *latest
				}
				return nil
			}
		}
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
	_, err := s.store.AppendEvent(ctx, tenantID, event)
	return err
}

func (s *RunService) failRun(ctx context.Context, tenantID string, run *domain.Run, err error) error {
	if run.Status == domain.RunStatusRunning {
		_ = run.Fail(err.Error())
		_ = s.checkpointRun(ctx, tenantID, run, "workflow.run_failed", err.Error())
	}
	return err
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
		resolved, selected, selectedSucceeded := 0, 0, 0
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
						return nil, nil, false, parseErr
					}
					chosen = conditionEdgeSelected(spec, edge.From, edge, value)
				}
				if chosen {
					selected++
					selectedSucceeded++
				}
			case domain.AttemptStatusFailed:
				return nil, nil, false, fmt.Errorf("upstream node %s failed", edge.From)
			}
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

func nodeInput(run *domain.Run, node domain.Node, states map[string]domain.NodeAttempt) (string, error) {
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
		return string(data), err
	}
	if len(incoming) == 1 && len(node.InputMapping) == 0 {
		return states[incoming[0]].OutputSummary, nil
	}
	inputs := map[string]any{"run_input": run.Input, "nodes": outputMap(states)}
	if len(node.InputMapping) > 0 {
		mapped := make(map[string]any, len(node.InputMapping))
		for key, reference := range node.InputMapping {
			if reference == "input" {
				mapped[key] = run.Input
				continue
			}
			parts := strings.Split(reference, ".")
			if len(parts) >= 3 && parts[0] == "nodes" && parts[2] == "output" {
				mapped[key] = states[parts[1]].OutputSummary
			}
		}
		inputs = mapped
	}
	data, err := json.Marshal(inputs)
	return string(data), err
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
