package handler

import (
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"go.uber.org/zap"
)

func TestNewSkillHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewSkillHandler(nil, logger, nil, nil)

	if handler == nil {
		t.Error("expected SkillHandler to be non-nil")
	}
}

func TestNewRAGHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewRAGHandler(nil, nil, nil, logger)

	if handler == nil {
		t.Error("expected RAGHandler to be non-nil")
	}
}

func TestNewAgentHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewAgentHandler(nil, logger, nil, observability.NoopMetrics{}, nil, nil, [32]byte{}, nil, nil, nil, nil, nil)

	if handler == nil {
		t.Error("expected AgentHandler to be non-nil")
	}
}

func TestNewMemoryHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewMemoryHandler(nil, logger)

	if handler == nil {
		t.Error("expected MemoryHandler to be non-nil")
	}
}

func TestExecuteAgentRequest_HasConversationIDField(t *testing.T) {
	req := ExecuteAgentRequest{
		Query:          "hello",
		ConversationID: "conv-111",
		UserID:         "user-999",
	}
	if req.ConversationID != "conv-111" {
		t.Errorf("expected ConversationID conv-111, got %s", req.ConversationID)
	}
	if req.UserID != "user-999" {
		t.Errorf("expected UserID user-999, got %s", req.UserID)
	}
}
