package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

type failingMemoryInjector struct{}

func (failingMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	return "", errors.New("memory store unavailable")
}

// TestExecute_FailsClosedWhenMemoryInjectionFails: memory retrieval is part of
// the execution contract when a MemoryInjector is configured — a failure must
// abort the execution before any LLM call, not silently run without memory.
func TestExecute_FailsClosedWhenMemoryInjectionFails(t *testing.T) {
	a := newReActAgent()
	a.MemoryInjector = failingMemoryInjector{}
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "42"}}}
	a.SetCapGateway(gw)

	_, err := a.Execute(context.Background(), "question",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory context")
	// The LLM must never be reached once context preparation failed.
	require.Empty(t, gw.requests)
}

// TestExecute_FailsClosedWhenHistoryLoadFails: conversation history is part of
// the execution context — a load failure must abort the execution before any
// LLM call, not silently run without conversation continuity.
func TestExecute_FailsClosedWhenHistoryLoadFails(t *testing.T) {
	a := newReActAgent()
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "42"}}}
	a.SetCapGateway(gw)
	a.WithChatStore(&mockChatStore{
		listMsgs: func(context.Context, string, string, string) ([]*agent.ChatMessage, error) {
			return nil, errors.New("db unavailable")
		},
	})

	_, err := a.Execute(context.Background(), "question",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversation history")
	require.Empty(t, gw.requests)
}

type recordingMemoryInjector struct {
	mu    sync.Mutex
	calls int
}

func (r *recordingMemoryInjector) BuildContext(context.Context, port.InjectionContext) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return "MEMORY_CONTEXT_MARKER", nil
}

func (r *recordingMemoryInjector) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// TestExecute_SystemAssistantMemoryInjectionNowEffective: 平台助手与普通 Agent
// 等同化后 SystemAssistantMode 不再跳过记忆注入——配置 MemoryInjector 时
// BuildContext 必须被调用，记忆上下文进入执行契约（此前 6 处跳过点已删）。
func TestExecute_SystemAssistantMemoryInjectionNowEffective(t *testing.T) {
	inj := &recordingMemoryInjector{}
	a := newReActAgent()
	a.MemoryInjector = inj
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "42"}}}
	a.SetCapGateway(gw)

	_, err := a.Execute(context.Background(), "question",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
	)
	require.NoError(t, err)
	require.Greater(t, inj.Calls(), 0, "平台助手必须参与记忆注入，不再整体跳过")
}

// TestExecute_SystemAssistantFailsClosedWhenMemoryInjectionFails: 平台助手模式
// 下记忆注入失败同样 fail-closed（与普通 Agent 一致的执行契约），禁止静默
// 无记忆运行。
func TestExecute_SystemAssistantFailsClosedWhenMemoryInjectionFails(t *testing.T) {
	a := newReActAgent()
	a.MemoryInjector = failingMemoryInjector{}
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "42"}}}
	a.SetCapGateway(gw)

	_, err := a.Execute(context.Background(), "question",
		agent.WithTenantID("t1"),
		agent.WithConversationID("conv-abc"),
		agent.WithUserID("user-1"),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory context")
	require.Empty(t, gw.requests)
}
