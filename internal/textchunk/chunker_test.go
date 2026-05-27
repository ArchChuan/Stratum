package textchunk

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestNewChunker(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	if chunker == nil {
		t.Error("expected chunker to be non-nil")
	}

	if chunker.maxChunkSize != 1000 {
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

func TestChunkTextLarge(t *testing.T) {
	logger := zap.NewNop()
	chunker := NewChunker(logger)

	// Create text larger than maxChunkSize
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("word ")
	}
	text := sb.String()

	chunks := chunker.ChunkText(text)

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	// Verify all chunks have content
	for i, chunk := range chunks {
		if chunk.Content == "" {
			t.Errorf("chunk %d has empty content", i)
		}
		if chunk.Index != i {
			t.Errorf("chunk %d has wrong index %d", i, chunk.Index)
		}
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
