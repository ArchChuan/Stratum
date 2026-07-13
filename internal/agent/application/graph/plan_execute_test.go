package graph_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func planJSON(steps []domain.PlanStep) string {
	b, _ := json.Marshal(steps)
	return string(b)
}

// stuckThenPlan simulates the lazy-planning sequence via tool-call cycling:
//   - First stuckRounds LLM calls return a dummy ToolCall (keeps graph looping).
//   - The tool node returns a fixed result.
//   - Once Steps >= stuckRounds the LLM call that follows has no tool calls → isStuck fires.
//   - reflect/plan/step/synthesize calls follow based on message content.
type stuckThenPlan struct {
	stuckRounds int
	plan        []domain.PlanStep
	stepAnswers []string // one per plan step (in order)
	finalAnswer string   // for synthesize node; empty = use last step answer
	llmCalls    int
}

func (s *stuckThenPlan) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.Type == port.CapSkill {
		return port.CapabilityResponse{Content: "skill result"}, nil
	}
	s.llmCalls++
	msg := ""
	if req.LLM != nil && len(req.LLM.Messages) > 0 {
		msg = req.LLM.Messages[len(req.LLM.Messages)-1].Content
	}

	switch {
	case strings.Contains(msg, "task planner") || strings.Contains(msg, "Break the task"):
		return port.CapabilityResponse{Content: planJSON(s.plan)}, nil

	case strings.Contains(msg, "Task goal:"):
		for i, step := range s.plan {
			if strings.Contains(msg, step.Goal) && i < len(s.stepAnswers) {
				return port.CapabilityResponse{Content: s.stepAnswers[i]}, nil
			}
		}
		return port.CapabilityResponse{Content: "step completed"}, nil

	case strings.Contains(msg, "Synthesize") || strings.Contains(msg, "Step results"):
		return port.CapabilityResponse{Content: s.finalAnswer}, nil

	case strings.Contains(msg, "got stuck") || strings.Contains(msg, "stuck after"):
		return port.CapabilityResponse{Content: "Stuck because context too broad; need to split into sub-goals."}, nil

	default:
		// ReAct main loop — return a dummy tool call to keep the graph cycling
		// until we've accumulated enough steps for isStuck to fire.
		if s.llmCalls <= s.stuckRounds {
			return port.CapabilityResponse{
				ToolCalls: []port.ToolCall{{ID: "t1", Name: "stratum_continue_reasoning", Arguments: map[string]any{}}},
			}, nil
		}
		// Steps >= StuckThreshold and no output → isStuck fires after this returns.
		return port.CapabilityResponse{Content: ""}, nil
	}
}

// ─── T1: Lazy planning trigger boundary ──────────────────────────────────────

// T1-1: simple task answers before threshold → no planning.
func TestPlanExecute_T1_SimpleTaskNoPlanning(t *testing.T) {
	stub := &capGWSequence{
		responses: []port.CapabilityResponse{{Content: "42"}},
	}
	cg, err := graph.BuildPlanExecuteGraph(stub, graph.NoopTokenRecorder{}, nil, nil, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		TenantID:       "t1",
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "what is 6*7?"}},
		StuckThreshold: 3,
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 5})
	require.NoError(t, err)
	assert.Equal(t, "42", out.Output)
	assert.False(t, out.PlanTriggered, "simple task should not trigger planning")
}

// T1-2: agent stalls for StuckThreshold rounds → planning triggered.
func TestPlanExecute_T1_StuckTriggersPlan(t *testing.T) {
	plan := []domain.PlanStep{
		{Goal: "search for relevant data"},
		{Goal: "summarise findings", DependsOn: []int{0}},
	}
	stub := &stuckThenPlan{
		stuckRounds: 3,
		plan:        plan,
		stepAnswers: []string{"search result", "final summary"},
		finalAnswer: "final answer",
	}
	cg, err := graph.BuildPlanExecuteGraph(stub, graph.NoopTokenRecorder{}, nil, nil, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		TenantID:       "t1",
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "complex research task"}},
		StuckThreshold: 3,
		MaxLLMSteps:    10,
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 30})
	require.NoError(t, err)
	assert.True(t, out.PlanTriggered)
	assert.NotEmpty(t, out.Plan)
	assert.NotEmpty(t, out.StepResults)
}

// T1-4: StuckThreshold=0 disables planning entirely.
func TestPlanExecute_T1_DisabledThreshold(t *testing.T) {
	// Returns empty 10 times, would trigger planning if threshold > 0.
	stub := &capGWSequence{
		infinite: port.CapabilityResponse{Content: ""},
		responses: []port.CapabilityResponse{
			{}, {}, {}, {}, {},
			{Content: "finally answered"},
		},
	}
	cg, err := graph.BuildPlanExecuteGraph(stub, graph.NoopTokenRecorder{}, nil, nil, zap.NewNop())
	require.NoError(t, err)

	state := graph.ReActState{
		TenantID:       "t1",
		Model:          "qwen-turbo",
		Messages:       []port.LLMMessage{{Role: "user", Content: "hard question"}},
		StuckThreshold: 0, // disabled
		MaxLLMSteps:    6,
	}
	out, err := cg.Invoke(context.Background(), state, graph.RunConfig{MaxSteps: 6})
	require.NoError(t, err)
	assert.False(t, out.PlanTriggered, "threshold=0 must never trigger planning")
}

// ─── T2: Wave scheduling ──────────────────────────────────────────────────────

func TestPlanExecute_T2_WaveSingleStep(t *testing.T) {
	waves := graph.ExportBuildWaves([]domain.PlanStep{
		{Goal: "only step"},
	})
	require.Len(t, waves, 1)
	assert.Equal(t, []int{0}, waves[0])
}

func TestPlanExecute_T2_Wave3Independent(t *testing.T) {
	// All steps independent → single wave with all 3.
	waves := graph.ExportBuildWaves([]domain.PlanStep{
		{Goal: "A"},
		{Goal: "B"},
		{Goal: "C"},
	})
	require.Len(t, waves, 1)
	assert.Len(t, waves[0], 3)
}

func TestPlanExecute_T2_WaveChain(t *testing.T) {
	// A → B → C: 3 waves.
	waves := graph.ExportBuildWaves([]domain.PlanStep{
		{Goal: "A"},
		{Goal: "B", DependsOn: []int{0}},
		{Goal: "C", DependsOn: []int{1}},
	})
	require.Len(t, waves, 3)
	assert.Equal(t, []int{0}, waves[0])
	assert.Equal(t, []int{1}, waves[1])
	assert.Equal(t, []int{2}, waves[2])
}

func TestPlanExecute_T2_WaveDiamond(t *testing.T) {
	// A, B independent → C depends on both: 2 waves.
	waves := graph.ExportBuildWaves([]domain.PlanStep{
		{Goal: "A"},
		{Goal: "B"},
		{Goal: "C", DependsOn: []int{0, 1}},
	})
	require.Len(t, waves, 2)
	assert.Len(t, waves[0], 2) // A and B
	assert.Equal(t, []int{2}, waves[1])
}

// ─── T3: Context isolation ────────────────────────────────────────────────────

// T3-1/T3-2: sub-step messages must contain only goal + prior summaries, not raw tool observations.
func TestPlanExecute_T3_ContextIsolation(t *testing.T) {
	parent := graph.ReActState{
		TenantID: "t1",
		Model:    "m",
		Messages: []port.LLMMessage{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "do complex task"},
			{Role: "assistant", Content: "calling tool", ToolCalls: []port.ToolCall{{ID: "x", Name: "fetch", Arguments: map[string]any{"url": "http://secret.internal"}}}},
			{Role: "tool", Content: "raw tool observation with secret data", ToolCallID: "x"},
		},
	}
	step := domain.PlanStep{Goal: "analyse data", HintTools: []string{"search"}}
	priorResults := []domain.StepResult{
		{StepIndex: 0, Goal: "fetch data", Summary: "fetched 10 records"},
	}

	stepState := graph.ExportBuildStepState(parent, step, 1, priorResults)

	// Must NOT contain raw tool observations.
	for _, m := range stepState.Messages {
		assert.NotContains(t, m.Content, "raw tool observation", "raw observations must not leak into sub-step context")
		assert.NotContains(t, m.Content, "http://secret.internal", "raw tool args must not leak")
	}

	// Must contain prior step summary.
	var found bool
	for _, m := range stepState.Messages {
		if strings.Contains(m.Content, "fetched 10 records") {
			found = true
		}
	}
	assert.True(t, found, "prior step summary must be injected into sub-step context")

	// Must contain step goal.
	var goalFound bool
	for _, m := range stepState.Messages {
		if strings.Contains(m.Content, "analyse data") {
			goalFound = true
		}
	}
	assert.True(t, goalFound, "sub-step user message must contain the step goal")

	// Sub-step must not inherit StuckThreshold (no nested planning).
	assert.Equal(t, 0, stepState.StuckThreshold)
}
