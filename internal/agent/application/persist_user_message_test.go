package application

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

// persistUserChatStub 记录 AddMessage 调用，验证用户消息的即时持久化行为。
type persistUserChatStub struct {
	ChatStore
	added []*ChatMessage
}

func (s *persistUserChatStub) AddMessage(_ context.Context, _ string, msg *ChatMessage) error {
	s.added = append(s.added, msg)
	return nil
}

func TestPersistUserMessage(t *testing.T) {
	base := &BaseAgent{Logger: zap.NewNop()}
	tr := noop.NewTracerProvider().Tracer("test")

	t.Run("saves user message immediately on first execution", func(t *testing.T) {
		store := &persistUserChatStub{}
		cfg := &ExecutionConfig{
			ConversationID: "conv-1", TenantID: "tenant-1", UserID: "user-1", TraceID: "trace-1",
		}
		base.persistUserMessage(context.Background(), tr, store, cfg, "帮我查订单", "agent-1", "scope-1")

		if len(store.added) != 1 {
			t.Fatalf("expected 1 saved user message, got %d", len(store.added))
		}
		got := store.added[0]
		if got.Role != "user" || got.Content != "帮我查订单" || got.ConversationID != "conv-1" || got.AgentID != "agent-1" {
			t.Fatalf("unexpected saved message: %+v", got)
		}
		if got.UserID != "user-1" || got.MemoryScope != "scope-1" || got.TraceID != "trace-1" {
			t.Fatalf("unexpected identity fields: %+v", got)
		}
	})

	t.Run("skips save on resume / reconnect (SkipUserMessageSave)", func(t *testing.T) {
		store := &persistUserChatStub{}
		cfg := &ExecutionConfig{ConversationID: "conv-1", TenantID: "tenant-1", SkipUserMessageSave: true}
		base.persistUserMessage(context.Background(), tr, store, cfg, "帮我查订单", "agent-1", "scope-1")

		if len(store.added) != 0 {
			t.Fatalf("expected no user message saved on resume, got %d", len(store.added))
		}
	})

	t.Run("skips save without conversation id", func(t *testing.T) {
		store := &persistUserChatStub{}
		base.persistUserMessage(context.Background(), tr, store, &ExecutionConfig{}, "帮我查订单", "agent-1", "scope-1")

		if len(store.added) != 0 {
			t.Fatalf("expected no user message saved without conversation, got %d", len(store.added))
		}
	})
}
