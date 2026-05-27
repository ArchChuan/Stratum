package handler

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewSkillHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewSkillHandler(nil, logger, nil)

	if handler == nil {
		t.Error("expected SkillHandler to be non-nil")
	}
}

func TestNewRAGHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewRAGHandler(nil, nil, logger)

	if handler == nil {
		t.Error("expected RAGHandler to be non-nil")
	}
}

func TestNewAgentHandler(t *testing.T) {
	logger := zap.NewNop()
	handler := NewAgentHandler(nil, logger, nil)

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
