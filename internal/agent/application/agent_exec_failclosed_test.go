package application_test

import (
	"context"
	"errors"
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
