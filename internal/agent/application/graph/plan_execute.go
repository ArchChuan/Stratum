package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	nodeReflect          = "reflect"
	nodePlan             = "plan"
	nodeCheckpointBridge = "checkpoint"
	nodeStepOrchestrator = "step_orchestrator"
	nodeSynthesize       = "synthesize"
	nodeMemoryStore      = "memory_store"

	planPhasePlanned  = "planned"
	planPhaseStepDone = "step_done"
)

// PlanCheckpointWriter is the minimum checkpoint interface required by the
// plan-execute graph. It mirrors port.CheckpointRepo.Upsert to avoid importing
// the full repository interface into the graph package.
type PlanCheckpointWriter interface {
	Upsert(ctx context.Context, tenantID string, cp domain.AgentExecutionCheckpoint) error
}

// isStuck returns true when the ReAct loop has stalled long enough to trigger
// the lazy-planning path. Requires StuckThreshold > 0 (opt-in).
func isStuck(s ReActState) bool {
	return s.StuckThreshold > 0 &&
		s.Steps >= s.StuckThreshold &&
		s.Output == "" &&
		!s.PlanTriggered
}

// BuildPlanExecuteGraph builds the hybrid ReAct+Plan graph.
// Simple tasks follow the standard LLM→tool loop (zero planning overhead).
// When the agent stalls for StuckThreshold rounds the graph transitions:
//
//	Reflect → Plan → Checkpoint → StepOrchestrator → Synthesize → MemoryStore
//
// checkpointWriter may be nil (disables checkpoint persistence).
// storeTemplateFn may be nil (disables plan template storage on success).
func BuildPlanExecuteGraph(
	capGW port.CapabilityGateway,
	ledger TokenRecorder,
	checkpointWriter PlanCheckpointWriter,
	storeTemplateFn func(ctx context.Context, tenantID, agentID string, plan []domain.PlanStep),
	logger *zap.Logger,
) (*CompiledGraph[ReActState], error) {
	g := New[ReActState]()

	// Standard ReAct base nodes.
	g.AddNode(nodeLLM, makeLLMNode(capGW, ledger, logger))
	g.AddNode(nodeTool, makeToolNode(capGW, logger))

	// Lazy-planning extension nodes.
	g.AddNode(nodeReflect, makeReflectNode(capGW, ledger, logger))
	g.AddNode(nodePlan, makePlanNode(capGW, ledger, logger))
	g.AddNode(nodeCheckpointBridge, makeCheckpointNode(checkpointWriter, planPhasePlanned, logger))
	g.AddNode(nodeStepOrchestrator, makeStepOrchestratorNode(capGW, ledger, checkpointWriter, logger))
	g.AddNode(nodeSynthesize, makeSynthesizeNode(capGW, ledger, logger))
	g.AddNode(nodeMemoryStore, makeMemoryStoreNode(storeTemplateFn, logger))

	// ReAct base loop.
	g.AddEdge(nodeTool, nodeLLM)

	// 3-way conditional from LLM node: tool-call | stuck → planning | done.
	g.AddConditionalEdge(nodeLLM, func(s ReActState) string {
		if len(s.Messages) > 0 {
			last := s.Messages[len(s.Messages)-1]
			if last.Role == "assistant" && len(last.ToolCalls) > 0 {
				return nodeTool
			}
		}
		if isStuck(s) {
			return nodeReflect
		}
		return END
	})

	// Planning path (linear after LLM determines stuck).
	g.AddEdge(nodeReflect, nodePlan)
	g.AddEdge(nodePlan, nodeCheckpointBridge)
	g.AddEdge(nodeCheckpointBridge, nodeStepOrchestrator)
	g.AddEdge(nodeStepOrchestrator, nodeSynthesize)
	g.AddEdge(nodeSynthesize, nodeMemoryStore)
	g.AddEdge(nodeMemoryStore, END)

	g.SetEntryPoint(nodeLLM)
	return g.Compile()
}

// makeReflectNode performs a single focused LLM call to understand why the
// agent stalled and identify what sub-goals would unblock progress.
func makeReflectNode(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		s.PlanTriggered = true

		var tried []string
		for _, tc := range s.AllToolCalls {
			tried = append(tried, tc.Name)
		}
		triedStr := "(none)"
		if len(tried) > 0 {
			triedStr = strings.Join(tried, ", ")
		}

		prompt := fmt.Sprintf(
			"An AI agent attempted to answer a user request but got stuck after %d reasoning rounds.\n"+
				"Tools attempted: %s\n\n"+
				"User request: %s\n\n"+
				"In 2-3 sentences: (1) explain why the agent is stuck, "+
				"(2) list the sub-goals that would allow a structured execution to succeed.",
			s.Steps, triedStr, firstUserMessage(s.Messages),
		)

		reflectCtx, cancel := context.WithTimeout(ctx, constants.AgentReflectTimeout)
		defer cancel()

		resp, err := RetryFn(reflectCtx, DefaultRetry, func() (port.CapabilityResponse, error) {
			return capGW.Route(reflectCtx, port.CapabilityRequest{
				TraceID:    s.TraceID,
				TenantID:   s.TenantID,
				Type:       port.CapLLM,
				LLMAPIKeys: s.LLMAPIKeys,
				LLM: &port.LLMCapRequest{
					Model:    s.Model,
					Messages: []port.LLMMessage{{Role: "user", Content: prompt}},
				},
			})
		})
		if err != nil {
			return s, fmt.Errorf("reflect node: %w", err)
		}
		total, cost := ledger.Record(reflectCtx, s.Model, resp.Usage)
		s.TotalTokens += total
		s.TotalCostUSD += cost
		s.ReflectionSummary = resp.Content
		logger.Info("react.reflect",
			zap.String("trace_id", s.TraceID),
			zap.String("summary", truncateRunes(resp.Content, 200)),
		)
		return s, nil
	}
}

// makePlanNode produces a structured execution plan via JSON-constrained LLM
// output. Prompt engineering enforces the PlanStep schema since the underlying
// LLM port does not expose a formal response_format parameter.
func makePlanNode(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		prompt := fmt.Sprintf(
			"You are a task planner. An AI agent got stuck and produced this analysis:\n%s\n\n"+
				"Original user request: %s\n\n"+
				"Break the task into at most %d ordered steps. Each step should be completable "+
				"by an AI agent using the available tools.\n\n"+
				"Reply with ONLY a valid JSON array — no markdown, no explanation:\n"+
				`[{"goal":"<step goal>","hint_tools":["optional_tool_name"],"depends_on":[0,1]}]`+
				"\n\nRules:\n"+
				"- goal: concise task description\n"+
				"- hint_tools: optional list of tool names that may be useful\n"+
				"- depends_on: zero-based indices of steps that must finish before this one (empty = parallel-safe)\n"+
				"- Return ONLY the JSON array, starting with [ and ending with ]",
			s.ReflectionSummary,
			firstUserMessage(s.Messages),
			constants.MaxPlanSteps,
		)

		planCtx, cancel := context.WithTimeout(ctx, constants.AgentPlanTimeout)
		defer cancel()

		resp, err := RetryFn(planCtx, DefaultRetry, func() (port.CapabilityResponse, error) {
			return capGW.Route(planCtx, port.CapabilityRequest{
				TraceID:    s.TraceID,
				TenantID:   s.TenantID,
				Type:       port.CapLLM,
				LLMAPIKeys: s.LLMAPIKeys,
				LLM: &port.LLMCapRequest{
					Model:    s.Model,
					Messages: []port.LLMMessage{{Role: "user", Content: prompt}},
				},
			})
		})
		if err != nil {
			return s, fmt.Errorf("plan node: %w", err)
		}
		total, cost := ledger.Record(planCtx, s.Model, resp.Usage)
		s.TotalTokens += total
		s.TotalCostUSD += cost

		raw := extractJSONArray(resp.Content)
		var steps []domain.PlanStep
		if err := json.Unmarshal([]byte(raw), &steps); err != nil {
			return s, fmt.Errorf("plan node: parse plan JSON: %w (content: %s)", err, truncateRunes(resp.Content, 300))
		}
		if len(steps) == 0 {
			return s, fmt.Errorf("plan node: LLM returned empty plan")
		}
		if len(steps) > constants.MaxPlanSteps {
			steps = steps[:constants.MaxPlanSteps]
		}
		s.Plan = steps
		logger.Info("react.plan",
			zap.String("trace_id", s.TraceID),
			zap.Int("steps", len(steps)),
		)
		return s, nil
	}
}

// makeCheckpointNode persists PlanRuntimeState and optionally signals the
// frontend via SSE. It is non-blocking: execution continues immediately.
func makeCheckpointNode(w PlanCheckpointWriter, phase string, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if w == nil {
			return s, nil
		}
		prs := domain.PlanRuntimeState{
			Phase:             phase,
			ReflectionSummary: s.ReflectionSummary,
			Plan:              s.Plan,
			PlanTemplateID:    s.PlanTemplateID,
			CurrentStepIndex:  s.CurrentStepIndex,
			StepResults:       s.StepResults,
		}
		stateJSON, err := json.Marshal(prs)
		if err != nil {
			logger.Warn("react.checkpoint.marshal", zap.Error(err))
			return s, nil
		}
		cp := domain.AgentExecutionCheckpoint{
			TraceID:          s.TraceID,
			RuntimeStateJSON: stateJSON,
			Status:           "active",
			ExpiresAt:        time.Now().Add(constants.PlanCheckpointTTL),
		}
		cpCtx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		defer cancel()
		if err := w.Upsert(cpCtx, s.TenantID, cp); err != nil {
			logger.Warn("react.checkpoint.upsert", zap.String("trace_id", s.TraceID), zap.Error(err))
		}
		// Notify frontend if SSE stream is active.
		if s.OnToken != nil && s.CheckpointEnabled {
			planJSON, _ := json.Marshal(s.Plan)
			s.OnToken(fmt.Sprintf("\n\nevent: plan_checkpoint\ndata: %s\n\n", planJSON))
		}
		return s, nil
	}
}

// makeStepOrchestratorNode executes the plan via wave scheduling.
// Steps with no DependsOn run concurrently in Wave 0; steps whose
// dependencies are fully resolved in prior waves run in subsequent waves.
// Each step runs a fresh sub-ReAct graph with a small LLM budget.
func makeStepOrchestratorNode(
	capGW port.CapabilityGateway,
	ledger TokenRecorder,
	w PlanCheckpointWriter,
	logger *zap.Logger,
) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.Plan) == 0 {
			return s, fmt.Errorf("step_orchestrator: empty plan")
		}

		subGraph, err := BuildReActGraph(capGW, ledger, logger)
		if err != nil {
			return s, fmt.Errorf("step_orchestrator: build sub-graph: %w", err)
		}

		waves := buildWaves(s.Plan)
		results := make([]domain.StepResult, len(s.Plan))

		for _, wave := range waves {
			if ctx.Err() != nil {
				return s, ctx.Err()
			}

			type stepOutcome struct {
				idx    int
				result domain.StepResult
				tokens int
				cost   float64
			}
			outcomes := make(chan stepOutcome, len(wave))
			var wg sync.WaitGroup

			for _, stepIdx := range wave {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					step := s.Plan[idx]
					stepState := buildStepState(s, step, idx, results)
					finalState, runErr := subGraph.Invoke(ctx, stepState, RunConfig{MaxSteps: constants.DefaultStepMaxLLMSteps})
					sr := domain.StepResult{
						StepIndex: idx,
						Goal:      step.Goal,
						Summary:   finalState.Output,
						Success:   finalState.Output != "" && runErr == nil,
					}
					if runErr != nil {
						sr.Error = runErr.Error()
					}
					outcomes <- stepOutcome{
						idx:    idx,
						result: sr,
						tokens: finalState.TotalTokens,
						cost:   finalState.TotalCostUSD,
					}
				}(stepIdx)
			}

			wg.Wait()
			close(outcomes)

			for o := range outcomes {
				results[o.idx] = o.result
				s.TotalTokens += o.tokens
				s.TotalCostUSD += o.cost
				s.CurrentStepIndex = o.idx
				logger.Info("react.step",
					zap.String("trace_id", s.TraceID),
					zap.Int("step", o.idx),
					zap.Bool("success", o.result.Success),
					zap.String("goal", truncateRunes(o.result.Goal, 100)),
				)
			}

			// Write checkpoint after each wave.
			if w != nil {
				s.StepResults = filterCompleted(results)
				_ = writeStepCheckpoint(ctx, w, s, planPhaseStepDone)
			}
		}

		s.StepResults = results
		return s, nil
	}
}

// makeSynthesizeNode produces the final answer from completed StepResults.
// If the last step in the plan has DependsOn covering all prior steps, its
// summary is already the aggregate answer and no extra LLM call is needed.
func makeSynthesizeNode(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.StepResults) == 0 {
			s.Output = "(no step results)"
			return s, nil
		}

		// Fast path: last step covers all predecessors → its summary is the answer.
		last := s.Plan[len(s.Plan)-1]
		if isCoveringStep(last, len(s.Plan)) && len(s.StepResults) > 0 {
			lastResult := s.StepResults[len(s.StepResults)-1]
			if lastResult.Success {
				s.Output = lastResult.Summary
				return s, nil
			}
		}

		// Aggregate all step summaries with a single LLM call.
		var summaries []string
		for _, r := range s.StepResults {
			if r.Summary != "" {
				summaries = append(summaries, fmt.Sprintf("Step %d (%s): %s", r.StepIndex, r.Goal, r.Summary))
			}
		}
		synthPrompt := fmt.Sprintf(
			"Synthesize the following step results into a coherent final answer for the user's original request:\n\n"+
				"User request: %s\n\nStep results:\n%s\n\n"+
				"Write a clear, comprehensive answer.",
			firstUserMessage(s.Messages),
			strings.Join(summaries, "\n"),
		)

		synthCtx, cancel := context.WithTimeout(ctx, constants.AgentSynthesizeTimeout)
		defer cancel()

		resp, err := RetryFn(synthCtx, DefaultRetry, func() (port.CapabilityResponse, error) {
			return capGW.Route(synthCtx, port.CapabilityRequest{
				TraceID:     s.TraceID,
				TenantID:    s.TenantID,
				Type:        port.CapLLM,
				LLMAPIKeys:  s.LLMAPIKeys,
				TokenStream: s.OnToken,
				LLM: &port.LLMCapRequest{
					Model:    s.Model,
					Messages: []port.LLMMessage{{Role: "user", Content: synthPrompt}},
				},
			})
		})
		if err != nil {
			// Degrade gracefully: join summaries rather than fail entirely.
			logger.Warn("react.synthesize.llm_failed", zap.String("trace_id", s.TraceID), zap.Error(err))
			s.Output = strings.Join(summaries, "\n\n")
			return s, nil
		}
		total, cost := ledger.Record(synthCtx, s.Model, resp.Usage)
		s.TotalTokens += total
		s.TotalCostUSD += cost
		s.Output = resp.Content
		return s, nil
	}
}

// makeMemoryStoreNode saves a successful plan as a reusable template so that
// similar future tasks can skip the reflection phase.
func makeMemoryStoreNode(
	storeTemplateFn func(ctx context.Context, tenantID, agentID string, plan []domain.PlanStep),
	logger *zap.Logger,
) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if storeTemplateFn == nil {
			return s, nil
		}
		allSuccess := len(s.StepResults) > 0
		for _, r := range s.StepResults {
			if !r.Success {
				allSuccess = false
				break
			}
		}
		if !allSuccess {
			return s, nil
		}
		// Fire-and-forget; don't block the response path. Detach from the
		// request's cancellation but keep its trace values (WithoutCancel),
		// then bound the store with its own timeout.
		detached := context.WithoutCancel(ctx)
		go func() {
			storeCtx, cancel := context.WithTimeout(detached, constants.AgentDBQueryTimeout)
			defer cancel()
			storeTemplateFn(storeCtx, s.TenantID, s.TraceID, s.Plan)
			logger.Info("react.memory_store",
				zap.String("trace_id", s.TraceID),
				zap.Int("plan_steps", len(s.Plan)),
			)
		}()
		return s, nil
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildWaves groups plan step indices into DAG execution waves.
// Wave 0 contains all steps with no dependencies; each subsequent wave
// contains steps whose dependencies are all satisfied by prior waves.
func buildWaves(plan []domain.PlanStep) [][]int {
	completed := make(map[int]bool)
	remaining := make([]int, len(plan))
	for i := range plan {
		remaining[i] = i
	}

	var waves [][]int
	for len(remaining) > 0 {
		var wave []int
		var next []int
		for _, idx := range remaining {
			if allDepsResolved(plan[idx].DependsOn, completed) {
				wave = append(wave, idx)
			} else {
				next = append(next, idx)
			}
		}
		if len(wave) == 0 {
			// Cycle or unresolvable deps — add all remaining to break deadlock.
			wave = next
			next = nil
		}
		for _, idx := range wave {
			completed[idx] = true
		}
		waves = append(waves, wave)
		remaining = next
	}
	return waves
}

func allDepsResolved(deps []int, completed map[int]bool) bool {
	for _, d := range deps {
		if !completed[d] {
			return false
		}
	}
	return true
}

// buildStepState constructs a minimal ReActState for a sub-step execution.
// Context isolation: only prior step summaries are injected, not raw tool observations.
func buildStepState(parent ReActState, step domain.PlanStep, stepIdx int, results []domain.StepResult) ReActState {
	var prevSummaries []string
	for _, r := range results[:stepIdx] {
		if r.Summary != "" {
			prevSummaries = append(prevSummaries, fmt.Sprintf("Step %d (%s): %s", r.StepIndex, r.Goal, r.Summary))
		}
	}

	userContent := "Task goal: " + step.Goal
	if len(prevSummaries) > 0 {
		userContent += "\n\nPrevious steps summary:\n" + strings.Join(prevSummaries, "\n")
	}
	if len(step.HintTools) > 0 {
		userContent += "\n\nSuggested tools: " + strings.Join(step.HintTools, ", ")
	}

	msgs := []port.LLMMessage{}
	if len(parent.Messages) > 0 && parent.Messages[0].Role == "system" {
		msgs = append(msgs, parent.Messages[0])
	}
	msgs = append(msgs, port.LLMMessage{Role: "user", Content: userContent})

	return ReActState{
		TenantID:                   parent.TenantID,
		TraceID:                    parent.TraceID,
		ConversationID:             parent.ConversationID,
		LLMAPIKeys:                 parent.LLMAPIKeys,
		Model:                      parent.Model,
		AvailableTools:             parent.AvailableTools,
		SkillCatalog:               parent.SkillCatalog,
		ActiveSkill:                parent.ActiveSkill,
		ToolCallFn:                 parent.ToolCallFn,
		ApprovalRequestFn:          parent.ApprovalRequestFn,
		ApprovedToolCallFn:         parent.ApprovedToolCallFn,
		ExecutionID:                parent.ExecutionID,
		AgentKnowledgeWorkspaceIDs: parent.AgentKnowledgeWorkspaceIDs,
		AgentMemoryScope:           parent.AgentMemoryScope,
		Messages:                   msgs,
		MaxLLMSteps:                constants.DefaultStepMaxLLMSteps,
		RAGSearchFn:                parent.RAGSearchFn,
		RecallMemoryFn:             parent.RecallMemoryFn,
		// No StuckThreshold — sub-steps use pure ReAct, no nested planning.
	}
}

// isCoveringStep returns true when step depends on ALL prior steps,
// meaning its output is already the aggregate of all results.
func isCoveringStep(step domain.PlanStep, totalSteps int) bool {
	if totalSteps <= 1 {
		return true
	}
	deps := make(map[int]bool, len(step.DependsOn))
	for _, d := range step.DependsOn {
		deps[d] = true
	}
	for i := 0; i < totalSteps-1; i++ {
		if !deps[i] {
			return false
		}
	}
	return true
}

// filterCompleted returns only StepResults at indices that have been populated.
func filterCompleted(results []domain.StepResult) []domain.StepResult {
	var out []domain.StepResult
	for _, r := range results {
		if r.Goal != "" {
			out = append(out, r)
		}
	}
	return out
}

// firstUserMessage extracts the first user-role content from the message list.
func firstUserMessage(msgs []port.LLMMessage) string {
	for _, m := range msgs {
		if m.Role == "user" {
			return truncateRunes(m.Content, 500)
		}
	}
	return "(unknown request)"
}

// extractJSONArray finds the first [...] block in s, handling model preamble.
func extractJSONArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

// writeStepCheckpoint serialises PlanRuntimeState into a checkpoint record.
func writeStepCheckpoint(ctx context.Context, w PlanCheckpointWriter, s ReActState, phase string) error {
	prs := domain.PlanRuntimeState{
		Phase:             phase,
		ReflectionSummary: s.ReflectionSummary,
		Plan:              s.Plan,
		PlanTemplateID:    s.PlanTemplateID,
		CurrentStepIndex:  s.CurrentStepIndex,
		StepResults:       s.StepResults,
	}
	b, err := json.Marshal(prs)
	if err != nil {
		return err
	}
	cp := domain.AgentExecutionCheckpoint{
		TraceID:          s.TraceID,
		RuntimeStateJSON: b,
		Status:           "active",
		ExpiresAt:        time.Now().Add(constants.PlanCheckpointTTL),
	}
	cpCtx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	defer cancel()
	return w.Upsert(cpCtx, s.TenantID, cp)
}
