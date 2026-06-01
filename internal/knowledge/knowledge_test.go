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

func TestValidateCypherIdentifier(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		// Valid identifiers
		{"Document", false},
		{"HAS_CHUNK", false},
		{"_private", false},
		{"Type123", false},
		{"a", false},
		// Invalid identifiers
		{"", true},
		{"has chunk", true},
		{"has-chunk", true},
		{"123start", true},
		{"type;DROP", true},
		{"label`injection", true},
		{"中文", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateCypherIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCypherIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
