package knowledge

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewGraphRAG(t *testing.T) {
	logger := zap.NewNop()
	rag := NewGraphRAG("bolt://localhost:7687", "neo4j", "password", logger)

	if rag == nil {
		t.Error("expected GraphRAG to be non-nil")
	}
}

func TestNewKnowledgeIngest(t *testing.T) {
	logger := zap.NewNop()
	ingest := NewKnowledgeIngest(nil, nil, nil, nil, nil, logger)

	if ingest == nil {
		t.Error("expected KnowledgeIngest to be non-nil")
	}
}

func TestNewRAGService(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, nil, logger)

	if service == nil {
		t.Error("expected RAGService to be non-nil")
	}
}
