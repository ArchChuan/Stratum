package port

import "context"

type TextChunk struct {
	Content  string
	ParentID string
}

type ChunkResult struct {
	Leaves  []TextChunk
	Parents []TextChunk
}

type ChunkingService interface {
	Clean(text string) string
	Chunk(ctx context.Context, text, strategy string, maxRunes, overlapRunes int, embedder Embedder) (ChunkResult, error)
	Filter(chunks []TextChunk) []TextChunk
}
