package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
)

// Mock for ChatStore interface
type mockChatStore struct {
	agent.ChatStore
	listMsgs func(ctx context.Context, tenantID, convID, userID string) ([]*agent.ChatMessage, error)
	addMsg   func(ctx context.Context, tenantID string, msg *agent.ChatMessage) error
}

func (m *mockChatStore) ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*agent.ChatMessage, error) {
	if m.listMsgs != nil {
		return m.listMsgs(ctx, tenantID, convID, userID)
	}
	return nil, nil
}

func (m *mockChatStore) AddMessage(ctx context.Context, tenantID string, msg *agent.ChatMessage) error {
	if m.addMsg != nil {
		return m.addMsg(ctx, tenantID, msg)
	}
	return nil
}

type failingPayloadStore struct{}

func (failingPayloadStore) Put(
	context.Context, port.TracePayload,
) (port.TracePayloadRef, error) {
	return port.TracePayloadRef{}, errors.New("minio unavailable")
}

// mockCapGW drives LLM responses in sequence; tools always succeed.
type mockCapGW struct {
	mu        sync.Mutex
	responses []port.CapabilityResponse
	requests  []port.CapabilityRequest
	idx       int
	err       error
}

func (m *mockCapGW) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if m.err != nil {
		return port.CapabilityResponse{}, m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.idx < len(m.responses) {
		r := m.responses[m.idx]
		m.idx++
		return r, nil
	}
	return port.CapabilityResponse{Content: "done"}, nil
}

func newReActAgent() *agent.BaseAgent {
	cfg := &agent.AgentConfig{
		ID:            "agent-001",
		Name:          "test-agent",
		Type:          agent.ReActAgent,
		LLMModel:      "qwen-turbo",
		SystemPrompt:  "You are helpful.",
		MaxIterations: 5,
	}
	return agent.NewBaseAgent(cfg, zap.NewNop())
}

func TestBaseAgent_ReActExecute_DirectAnswer(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []port.CapabilityResponse{
		{Content: "42", Usage: port.TokenUsage{Total: 20}},
	}}
	a.SetCapGateway(gw)

	result, err := a.Execute(context.Background(), "what is 6x7?",
		agent.WithTenantID("t1"),
	)
	require.NoError(t, err)
	require.Equal(t, "42", result.Output)
	require.Equal(t, "agent-001", result.AgentID)
	require.Equal(t, 1, result.Steps)
	require.Equal(t, 20, result.TokensUsed)
}

func TestBaseAgent_ReActExecute_WithToolCall(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
	}
	a.SetCapGateway(gw)

	result, err := a.Execute(context.Background(), "calc 6*7",
		agent.WithTenantID("t1"),
		agent.WithMaxSteps(10),
		agent.WithExtraTools([]port.ToolDefinition{{Name: "calc", ProviderType: "mcp", ServerID: "math", Metadata: map[string]any{"risk_level": "read"}}}),
		agent.WithToolExecutionFn(func(context.Context, port.ToolExecutionRequest) (any, error) {
			return port.GuardedToolResult{ModelContent: "42"}, nil
		}),
	)
	require.NoError(t, err)
	require.Equal(t, "The answer is 42", result.Output)
	require.Equal(t, 2, result.Steps)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "calc", result.ToolCalls[0].ToolName)
}

func TestBaseAgentPayloadStoreFailureDoesNotFailExecution(t *testing.T) {
	t.Setenv("OTEL_CAPTURE_CONTENT", "true")
	a := newReActAgent()
	a.SetCapGateway(&mockCapGW{responses: []port.CapabilityResponse{{Content: "answer"}}})
	result, err := a.Execute(context.Background(), "question",
		agent.WithTenantID("tenant-1"),
		agent.WithTraceID("trace-1"),
		agent.WithTracePayloadStore(failingPayloadStore{}),
	)
	require.NoError(t, err)
	require.Equal(t, "answer", result.Output)
}

func TestBaseAgentOTelHierarchyFollowsReActGraphContext(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	a := newReActAgent()
	a.SetCapGateway(&mockCapGW{responses: []port.CapabilityResponse{
		{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}, Usage: port.TokenUsage{Prompt: 11, Completion: 3, Total: 14}},
		{Content: "The answer is 42", Usage: port.TokenUsage{Prompt: 17, Completion: 5, Total: 22}},
	}})
	_, err := a.Execute(context.Background(), "calc 6*7",
		agent.WithTenantID("tenant-1"),
		agent.WithExtraTools([]port.ToolDefinition{{
			Name: "calc", ProviderType: "mcp", ProviderID: "math", ServerID: "math", CapabilityID: "calculate",
			Metadata: map[string]any{"risk_level": "read", "version_id": "tool-revision-1"},
		}}),
		agent.WithToolExecutionFn(func(context.Context, port.ToolExecutionRequest) (any, error) {
			return port.GuardedToolResult{ModelContent: "42"}, nil
		}),
	)
	require.NoError(t, err)

	spans := recorder.Ended()
	byName := make(map[string][]sdktrace.ReadOnlySpan)
	for _, span := range spans {
		byName[span.Name()] = append(byName[span.Name()], span)
	}
	require.Len(t, byName["agent.execute"], 1)
	require.Len(t, byName["react.graph.invoke"], 1)
	require.Len(t, byName["react.llm"], 2)
	require.Len(t, byName["react.tool"], 1)

	rootID := byName["agent.execute"][0].SpanContext().SpanID()
	graph := byName["react.graph.invoke"][0]
	require.Equal(t, rootID, graph.Parent().SpanID())
	for _, name := range []string{"react.llm", "react.tool"} {
		for _, span := range byName[name] {
			require.Equal(t, graph.SpanContext().SpanID(), span.Parent().SpanID(), name)
		}
	}
	firstLLM := spanAttributes(byName["react.llm"][0])
	require.Equal(t, "qwen-turbo", firstLLM["gen_ai.request.model"])
	require.Equal(t, int64(11), firstLLM["gen_ai.usage.input_tokens"])
	require.Equal(t, int64(3), firstLLM["gen_ai.usage.output_tokens"])
	require.NotEmpty(t, firstLLM["stratum.input.sha256"])
	require.NotEmpty(t, firstLLM["stratum.output.sha256"])
	toolAttrs := spanAttributes(byName["react.tool"][0])
	require.Equal(t, "c1", toolAttrs["gen_ai.tool.call.id"])
	require.Equal(t, "calc", toolAttrs["gen_ai.tool.name"])
	require.Equal(t, "mcp", toolAttrs["stratum.provider.type"])
	require.Equal(t, "math", toolAttrs["stratum.server.id"])
	require.Equal(t, "calculate", toolAttrs["stratum.capability.id"])
	require.Equal(t, "tool-revision-1", toolAttrs["stratum.resource.revision_id"])
	require.Equal(t, "tenant-1", toolAttrs["opik.metadata.stratum.tenant_id"])
	require.Equal(t, "mcp", toolAttrs["opik.metadata.stratum.provider_type"])
	require.Equal(t, "tool-revision-1", toolAttrs["opik.metadata.stratum.resource_revision_id"])
	require.NotEmpty(t, toolAttrs["stratum.arguments.sha256"])
	require.NotEmpty(t, toolAttrs["stratum.result.sha256"])
}

func TestBaseAgentRootSpanCarriesEvaluationEvidence(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	a := newReActAgent()
	a.SetCapGateway(&mockCapGW{responses: []port.CapabilityResponse{{Content: "42"}}})
	_, err := a.Execute(context.Background(), "what is 6x7?",
		agent.WithTenantID("tenant-1"),
		agent.WithUserID("user-1"),
		agent.WithTraceID("business-trace-1"),
		agent.WithExecutionID("execution-1"),
		agent.WithConversationID("conversation-1"),
		agent.WithEvolutionTraceMetadata(agent.EvolutionTraceMetadata{
			Evaluation:   true,
			ExperimentID: "experiment-1",
			Variant:      "canary",
			ExperimentAssignments: map[string]agent.ExperimentAssignment{
				"skill:skill-1": {ExperimentID: "experiment-1", Variant: "canary"},
			},
			ResourceManifest: map[string]string{
				"agent:agent-001": "agent-revision-1",
				"skill:skill-1":   "skill-revision-2",
			},
		}),
	)
	require.NoError(t, err)

	var root sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "agent.execute" {
			root = span
			break
		}
	}
	require.NotNil(t, root)
	attrs := spanAttributes(root)
	require.Equal(t, "tenant-1", attrs["stratum.tenant.id"])
	require.Equal(t, "user-1", attrs["stratum.user.id"])
	require.Equal(t, "business-trace-1", attrs["stratum.trace.id"])
	require.Equal(t, "execution-1", attrs["stratum.execution.id"])
	require.Equal(t, "conversation-1", attrs["stratum.conversation.id"])
	require.Equal(t, "tenant-1", attrs["opik.metadata.stratum.tenant_id"])
	require.Equal(t, "business-trace-1", attrs["opik.metadata.stratum.trace_id"])
	require.Equal(t, "execution-1", attrs["opik.metadata.stratum.execution_id"])
	require.Equal(t, "true", attrs["stratum.evaluation"])
	require.Equal(t, "experiment-1", attrs["stratum.experiment.id"])
	require.Equal(t, "canary", attrs["stratum.experiment.variant"])
	var assignments map[string]agent.ExperimentAssignment
	require.NoError(t, json.Unmarshal([]byte(attrs["stratum.experiment.assignments"].(string)), &assignments))
	require.Equal(t, "experiment-1", assignments["skill:skill-1"].ExperimentID)
	require.Equal(t, "success", attrs["opik.metadata.stratum.status"])
	require.Equal(t, int64(0), attrs["opik.metadata.stratum.total_tokens"])
	require.Contains(t, attrs, "opik.metadata.stratum.duration_ms")
	require.Contains(t, attrs, "opik.metadata.stratum.cost_usd")
	var manifest map[string]string
	require.NoError(t, json.Unmarshal([]byte(attrs["stratum.resource.manifest"].(string)), &manifest))
	require.Equal(t, "skill-revision-2", manifest["skill:skill-1"])
}

func spanAttributes(span sdktrace.ReadOnlySpan) map[string]any {
	out := make(map[string]any)
	for _, attr := range span.Attributes() {
		out[string(attr.Key)] = attr.Value.AsInterface()
	}
	return out
}

func TestBaseAgent_ReActExecute_CapGWNil(t *testing.T) {
	a := newReActAgent()
	// no SetCapGateway call → CapGateway is nil

	_, err := a.Execute(context.Background(), "hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CapGateway not set")
}

func TestBaseAgent_ReActExecute_LLMError(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{err: errors.New("llm unavailable")}
	a.SetCapGateway(gw)

	_, err := a.Execute(context.Background(), "hello")
	require.Error(t, err)
}

func TestBaseAgentOTelMarksLLMFailureAsError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	a := newReActAgent()
	a.SetCapGateway(&mockCapGW{err: errors.New("llm unavailable")})
	_, err := a.Execute(context.Background(), "hello", agent.WithTenantID("tenant-1"))
	require.Error(t, err)

	for _, span := range recorder.Ended() {
		if span.Name() == "react.llm" {
			require.Equal(t, codes.Error, span.Status().Code)
			return
		}
	}
	t.Fatal("react.llm span not found")
}

func TestWithConversationID_SetsField(t *testing.T) {
	cfg := &agent.ExecutionConfig{}
	agent.WithConversationID("conv-123")(cfg)
	require.Equal(t, "conv-123", cfg.ConversationID)
}

func TestWithUserID_SetsField(t *testing.T) {
	cfg := &agent.ExecutionConfig{}
	agent.WithUserID("user-456")(cfg)
	require.Equal(t, "user-456", cfg.UserID)
}

func TestWithExecutionID_SetsField(t *testing.T) {
	cfg := &agent.ExecutionConfig{}
	agent.WithExecutionID("exec-123")(cfg)
	require.Equal(t, "exec-123", cfg.ExecutionID)
}

func TestWithHistoryWindow_SetsField(t *testing.T) {
	cfg := &agent.ExecutionConfig{}
	agent.WithHistoryWindow(10)(cfg)
	require.Equal(t, 10, cfg.HistoryWindow)
}

func TestBaseAgent_SetCapGateway_DataRace(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "ok"}}}
	var wg sync.WaitGroup
	// concurrent SetCapGateway + Execute
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			a.SetCapGateway(gw)
		}()
		go func() {
			defer wg.Done()
			_, _ = a.Execute(context.Background(), "ping")
		}()
	}
	wg.Wait()
}

func TestBaseAgent_WithChatStore_SetsField(t *testing.T) {
	a := newReActAgent()
	cs := &mockChatStore{}
	result := a.WithChatStore(cs)
	require.NotNil(t, result)
}

func TestExecute_PersistsMessagesToChatStore(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []port.CapabilityResponse{
			{Content: "six", Usage: port.TokenUsage{Total: 5}},
		},
	}
	a.SetCapGateway(gw)

	var savedMsgs []*agent.ChatMessage
	cs := &mockChatStore{
		addMsg: func(ctx context.Context, tenantID string, msg *agent.ChatMessage) error {
			saved := *msg
			savedMsgs = append(savedMsgs, &saved)
			return nil
		},
	}
	a.WithChatStore(cs)

	_, err := a.Execute(context.Background(), "what is 3+3?",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-xyz"),
		agent.WithUserID("user-2"),
	)
	require.NoError(t, err)
	require.Len(t, savedMsgs, 2)
	require.Equal(t, "user", savedMsgs[0].Role)
	require.Equal(t, "what is 3+3?", savedMsgs[0].Content)
	require.Equal(t, "assistant", savedMsgs[1].Role)
	require.Equal(t, "six", savedMsgs[1].Content)
	require.Equal(t, "conv-xyz", savedMsgs[0].ConversationID)
	require.Equal(t, "conv-xyz", savedMsgs[1].ConversationID)
}

func TestExecute_ReturnsToolTraceAndPersistsSummaryMessage(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []port.CapabilityResponse{
			{ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42", Usage: port.TokenUsage{Total: 10}},
		},
	}
	a.SetCapGateway(gw)

	var savedMsgs []*agent.ChatMessage
	cs := &mockChatStore{
		addMsg: func(ctx context.Context, tenantID string, msg *agent.ChatMessage) error {
			saved := *msg
			savedMsgs = append(savedMsgs, &saved)
			return nil
		},
	}
	a.WithChatStore(cs)

	result, err := a.Execute(context.Background(), "calc 6*7",
		agent.WithTenantID("t1"),
		agent.WithTraceID("trace-1"),
		agent.WithExecutionID("exec-1"),
		agent.WithConversationID("conv-xyz"),
		agent.WithUserID("user-2"),
		agent.WithMaxSteps(10),
		agent.WithExtraTools([]port.ToolDefinition{{Name: "calc", ProviderType: "mcp", ServerID: "math", Metadata: map[string]any{"risk_level": "read"}}}),
		agent.WithToolExecutionFn(func(context.Context, port.ToolExecutionRequest) (any, error) {
			return port.GuardedToolResult{ModelContent: "42"}, nil
		}),
	)
	require.NoError(t, err)
	require.Len(t, result.ToolObservations, 1)
	require.Equal(t, "c1", result.ToolObservations[0].ToolCallID)
	require.Equal(t, "exec-1", result.ToolObservations[0].ExecutionID)
	require.Equal(t, "calc", result.ToolObservations[0].ToolName)
	require.Equal(t, "42", result.ToolObservations[0].RawText)
	require.NotEmpty(t, result.TraceEvents)
	for _, ev := range result.TraceEvents {
		require.Equal(t, "exec-1", ev.ExecutionID)
	}

	require.Len(t, savedMsgs, 3)
	require.Equal(t, "assistant", savedMsgs[2].Role)
	require.Contains(t, savedMsgs[2].Content, "本轮工具观察摘要")
	require.Contains(t, savedMsgs[2].Content, "calc")
	require.True(t, savedMsgs[2].SkipOutbox)
}

func TestExecute_LoadsHistoryFromChatStore(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []port.CapabilityResponse{
			{Content: "I remember you asked before", Usage: port.TokenUsage{Total: 5}},
		},
	}
	a.SetCapGateway(gw)

	history := []*agent.ChatMessage{
		{Role: "user", Content: "what is 2+2?"},
		{Role: "assistant", Content: "2+2=4"},
	}
	cs := &mockChatStore{
		listMsgs: func(ctx context.Context, tenantID, convID, userID string) ([]*agent.ChatMessage, error) {
			return history, nil
		},
	}
	a.WithChatStore(cs)

	result, err := a.Execute(context.Background(), "and 3+3?",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
	)
	require.NoError(t, err)
	require.Equal(t, "I remember you asked before", result.Output)
}

func TestExecute_CompactsOverflowingInitialHistory(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "done"}}}
	compactor := &fakeCompactor{summary: "compacted earlier discussion"}
	a.SetCapGateway(gw)
	a.SetHistoryCompactor(compactor)

	history := makeHistory(12)
	a.WithChatStore(&mockChatStore{
		listMsgs: func(context.Context, string, string, string) ([]*agent.ChatMessage, error) {
			return history, nil
		},
	})

	_, err := a.Execute(
		context.Background(),
		"continue",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
		agent.WithHistoryWindow(4),
	)
	require.NoError(t, err)
	require.Equal(t, 1, compactor.callCount)
	require.Equal(t, 8, compactor.gotMsgs)
	require.Len(t, gw.requests, 1)
	require.NotNil(t, gw.requests[0].LLM)
	require.True(t, strings.Contains(gw.requests[0].LLM.Messages[0].Content, compactor.summary))
}

func TestBuildInitMessages_EmptyHistory(t *testing.T) {
	msgs := agent.BuildInitMessages("You are helpful.", nil, 0)
	require.Len(t, msgs, 1)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "You are helpful.", msgs[0].Content)
}

func TestBuildInitMessages_PreservesAssistantRole(t *testing.T) {
	history := []*agent.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	msgs := agent.BuildInitMessages("sys", history, 10)
	require.Len(t, msgs, 3)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "user", msgs[1].Role)
	require.Equal(t, "assistant", msgs[2].Role)
}

func TestBuildInitMessages_WindowTruncation(t *testing.T) {
	history := make([]*agent.ChatMessage, 25)
	for i := range history {
		history[i] = &agent.ChatMessage{Role: "user", Content: "msg"}
	}
	msgs := agent.BuildInitMessages("", history, 20)
	// 20 history + 0 system (empty string)
	require.Len(t, msgs, 20)
}

func TestBuildInitMessages_DefaultWindow(t *testing.T) {
	history := make([]*agent.ChatMessage, 25)
	for i := range history {
		history[i] = &agent.ChatMessage{Role: "user", Content: "msg"}
	}
	msgs := agent.BuildInitMessages("sys", history, 0) // 0 → default 20
	require.Len(t, msgs, 21)                           // 20 history + 1 system
}

func TestBaseAgent_AddToMemory_StillAddsToSlice(t *testing.T) {
	a := newReActAgent()
	a.AddToMemory(agent.Message{Role: "user", Content: "hello"})
	mem := a.GetMemory()
	require.Len(t, mem, 1)
	require.Equal(t, "user", mem[0].Role)
	require.Equal(t, "hello", mem[0].Content)
}
