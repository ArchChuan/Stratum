package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/stretchr/testify/require"
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

// mockCapGW drives LLM responses in sequence; tools always succeed.
type mockCapGW struct {
	mu        sync.Mutex
	responses []capgateway.CapabilityResponse
	idx       int
	toolResp  capgateway.CapabilityResponse
	err       error
}

func (m *mockCapGW) Route(_ context.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	if m.err != nil {
		return capgateway.CapabilityResponse{}, m.err
	}
	if req.Type == capgateway.CapSkill {
		return m.toolResp, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx < len(m.responses) {
		r := m.responses[m.idx]
		m.idx++
		return r, nil
	}
	return capgateway.CapabilityResponse{Content: "done"}, nil
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
	gw := &mockCapGW{responses: []capgateway.CapabilityResponse{
		{Content: "42", Usage: capgateway.TokenUsage{Total: 20}},
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
		responses: []capgateway.CapabilityResponse{
			{ToolCalls: []capgateway.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{"expr": "6*7"}}}},
			{Content: "The answer is 42"},
		},
		toolResp: capgateway.CapabilityResponse{Content: "42"},
	}
	a.SetCapGateway(gw)

	result, err := a.Execute(context.Background(), "calc 6*7",
		agent.WithTenantID("t1"),
		agent.WithMaxSteps(10),
	)
	require.NoError(t, err)
	require.Equal(t, "The answer is 42", result.Output)
	require.Equal(t, 2, result.Steps)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "calc", result.ToolCalls[0].ToolName)
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

func TestWithHistoryWindow_SetsField(t *testing.T) {
	cfg := &agent.ExecutionConfig{}
	agent.WithHistoryWindow(10)(cfg)
	require.Equal(t, 10, cfg.HistoryWindow)
}

func TestBaseAgent_SetCapGateway_DataRace(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []capgateway.CapabilityResponse{{Content: "ok"}}}
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
		responses: []capgateway.CapabilityResponse{
			{Content: "six", Usage: capgateway.TokenUsage{Total: 5}},
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
	require.Equal(t, "agent", savedMsgs[1].Role)
	require.Equal(t, "six", savedMsgs[1].Content)
	require.Equal(t, "conv-xyz", savedMsgs[0].ConversationID)
	require.Equal(t, "conv-xyz", savedMsgs[1].ConversationID)
}

func TestExecute_LoadsHistoryFromChatStore(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{
		responses: []capgateway.CapabilityResponse{
			{Content: "I remember you asked before", Usage: capgateway.TokenUsage{Total: 5}},
		},
	}
	a.SetCapGateway(gw)

	history := []*agent.ChatMessage{
		{Role: "user", Content: "what is 2+2?"},
		{Role: "agent", Content: "2+2=4"},
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

func TestBuildInitMessages_EmptyHistory(t *testing.T) {
	msgs := agent.BuildInitMessages("You are helpful.", nil, 0)
	require.Len(t, msgs, 1)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "You are helpful.", msgs[0].Content)
}

func TestBuildInitMessages_NormalizesAgentRole(t *testing.T) {
	history := []*agent.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "agent", Content: "hi there"},
	}
	msgs := agent.BuildInitMessages("sys", history, 10)
	require.Len(t, msgs, 3)
	require.Equal(t, "system", msgs[0].Role)
	require.Equal(t, "user", msgs[1].Role)
	require.Equal(t, "assistant", msgs[2].Role) // "agent" → "assistant"
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
