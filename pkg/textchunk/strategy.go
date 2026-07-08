package textchunk

import "context"

// Embedder abstracts the vector embedding API for strategies that need it at chunk time.
type Embedder interface {
	EmbedVector(ctx context.Context, text string) ([]float32, error)
}

// ChunkResult is the output of any chunking Strategy.
// Leaves are always populated; Parents is non-empty only for Parent-Child strategies.
type ChunkResult struct {
	// Leaves are the small retrieval units — embedded and stored in Milvus + PG.
	Leaves []TextChunk
	// Parents are the larger context units stored only in PG.
	// Each Leaf whose ParentID is set references a parent here by its ID.
	Parents []TextChunk
}

// Strategy is the interface all chunking algorithms implement.
// embedder may be nil for non-semantic strategies; implementations must tolerate that.
type Strategy interface {
	// Name returns the strategy identifier (matches domain.ChunkingStrategy* constants).
	Name() string
	// Chunk splits text into a ChunkResult. maxRunes caps the leaf chunk size.
	// embedder is only used by SemanticStrategy; other strategies ignore it.
	Chunk(ctx context.Context, text string, maxRunes, overlapRunes int, embedder Embedder) ChunkResult
}
