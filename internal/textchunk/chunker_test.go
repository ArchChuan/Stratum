package textchunk

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestNewChunker(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	if chunker == nil { //nolint:staticcheck
		t.Error("expected chunker to be non-nil")
	}

	if chunker.maxChunkSize != 1000 { //nolint:staticcheck
		t.Errorf("expected maxChunkSize 1000, got %d", chunker.maxChunkSize)
	}

	if chunker.overlapSize != 200 {
		t.Errorf("expected overlapSize 200, got %d", chunker.overlapSize)
	}

	if chunker.minChunkSize != 100 {
		t.Errorf("expected minChunkSize 100, got %d", chunker.minChunkSize)
	}
}

func TestChunkTextSmall(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	text := "This is a small text"
	chunks := chunker.ChunkText(text)

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}

	if chunks[0].Content != text {
		t.Errorf("expected content %s, got %s", text, chunks[0].Content)
	}

	if chunks[0].Index != 0 {
		t.Errorf("expected index 0, got %d", chunks[0].Index)
	}
}

func TestChunkTextEmpty(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	chunks := chunker.ChunkText("")

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for empty text, got %d", len(chunks))
	}

	if chunks[0].Content != "" {
		t.Errorf("expected empty content, got %s", chunks[0].Content)
	}
}

func TestTextChunkStruct(t *testing.T) {
	chunk := TextChunk{
		Content:    "test content",
		Index:      5,
		SourceText: "original source text",
	}

	if chunk.Content != "test content" {
		t.Errorf("expected content 'test content', got %s", chunk.Content)
	}

	if chunk.Index != 5 {
		t.Errorf("expected index 5, got %d", chunk.Index)
	}

	if chunk.SourceText != "original source text" {
		t.Errorf("expected source text, got %s", chunk.SourceText)
	}
}

func TestChunkTextWithPunctuation(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	text := "Hello. World. This is a test."
	chunks := chunker.ChunkText(text)

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	for _, chunk := range chunks {
		if len(chunk.Content) == 0 {
			t.Error("expected non-empty chunk content")
		}
	}
}

func TestChunkTextMultipleChunks(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	text := strings.Repeat("word ", 50)
	chunks := chunker.ChunkText(text)

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	for i, chunk := range chunks {
		if chunk.Index != i {
			t.Errorf("chunk index mismatch: expected %d, got %d", i, chunk.Index)
		}
	}
}
