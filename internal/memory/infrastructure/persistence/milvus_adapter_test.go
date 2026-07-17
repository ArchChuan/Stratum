package persistence

import (
	"encoding/json"
	"strings"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

func TestMemoryFactDocumentChunkPreservesWhitelistedMetadata(t *testing.T) {
	doc := &memport.VectorDoc{
		ID:        "fact-1",
		Embedding: []float32{0.1, 0.2},
		Metadata: map[string]interface{}{
			"user_id":         "user-1",
			"agent_id":        "agent-1",
			"scope":           "agent",
			"content":         "likes Go",
			"conversation_id": "conversation-1",
			"importance":      0.75,
			"category":        "skill",
			"confidence":      0.875,
			"source":          "llm_extraction",
			"api_key":         "must-not-leak",
			"arbitrary":       map[string]string{"secret": "must-not-leak"},
		},
	}

	chunk, err := memoryFactDocumentChunk(doc)
	if err != nil {
		t.Fatalf("memoryFactDocumentChunk: %v", err)
	}
	if chunk.ID != doc.ID || chunk.UserID != "user-1" || chunk.AgentID != "agent-1" ||
		chunk.Scope != "agent" || chunk.Content != "likes Go" || len(chunk.Vector) != 2 {
		t.Fatalf("chunk fields not preserved: %#v", chunk)
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(chunk.SourceDocument), &metadata); err != nil {
		t.Fatalf("source document is not JSON: %v", err)
	}
	wantKeys := []string{"conversation_id", "importance", "category", "confidence", "source"}
	if len(metadata) != len(wantKeys) {
		t.Fatalf("metadata keys = %v, want only %v", metadata, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := metadata[key]; !ok {
			t.Errorf("missing metadata key %q", key)
		}
	}
	if metadata["importance"] != 0.75 || metadata["confidence"] != 0.875 {
		t.Fatalf("numeric metadata changed type/value: %#v", metadata)
	}
	if strings.Contains(chunk.SourceDocument, "api_key") || strings.Contains(chunk.SourceDocument, "must-not-leak") || strings.Contains(chunk.SourceDocument, "arbitrary") {
		t.Fatalf("source document copied non-whitelisted metadata: %s", chunk.SourceDocument)
	}

	second, err := memoryFactDocumentChunk(doc)
	if err != nil {
		t.Fatal(err)
	}
	if second.SourceDocument != chunk.SourceDocument {
		t.Fatalf("JSON is not deterministic: %q != %q", second.SourceDocument, chunk.SourceDocument)
	}
}

func TestMemoryFactDocumentChunkRejectsInvalidMetadata(t *testing.T) {
	doc := validMemoryFactVectorDoc()
	doc.Metadata["confidence"] = "high"

	_, err := memoryFactDocumentChunk(doc)
	if err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("error = %v, want clear confidence metadata error", err)
	}
}

func TestMemoryFactDocumentChunkRejectsOversizedSourceDocument(t *testing.T) {
	doc := validMemoryFactVectorDoc()
	doc.Metadata["conversation_id"] = strings.Repeat("x", 256)

	_, err := memoryFactDocumentChunk(doc)
	if err == nil || !strings.Contains(err.Error(), "source_document") || !strings.Contains(err.Error(), "255") {
		t.Fatalf("error = %v, want clear source_document length error", err)
	}
}

func validMemoryFactVectorDoc() *memport.VectorDoc {
	return &memport.VectorDoc{
		ID:        "fact-1",
		Embedding: []float32{1},
		Metadata: map[string]interface{}{
			"user_id":         "user-1",
			"agent_id":        "agent-1",
			"scope":           "user",
			"content":         "content",
			"conversation_id": "conversation-1",
			"importance":      0.5,
			"category":        "other",
			"confidence":      0.8,
			"source":          "llm_extraction",
		},
	}
}
