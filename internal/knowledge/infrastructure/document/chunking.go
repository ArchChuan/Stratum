package document

import (
	"context"
	"fmt"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
)

type ChunkingService struct {
	cleaner    *textchunk.TextCleaner
	strategies map[string]textchunk.Strategy
}

func NewChunkingService() *ChunkingService {
	return &ChunkingService{
		cleaner: textchunk.NewTextCleaner(),
		strategies: map[string]textchunk.Strategy{
			"recursive":           textchunk.NewRecursiveStrategy(),
			"structure_recursive": textchunk.NewStructureRecursiveStrategy(),
			"semantic":            textchunk.NewSemanticStrategy(),
		},
	}
}

func (s *ChunkingService) Clean(text string) string { return s.cleaner.Clean(text) }

func (s *ChunkingService) Chunk(
	ctx context.Context,
	text, strategy string,
	maxRunes, overlapRunes int,
	embedder knowledgeport.Embedder,
) (knowledgeport.ChunkResult, error) {
	implementation, ok := s.strategies[strategy]
	if !ok {
		return knowledgeport.ChunkResult{}, fmt.Errorf("unknown chunking strategy: %s", strategy)
	}
	result := implementation.Chunk(ctx, text, maxRunes, overlapRunes, embedder)
	return fromTextChunkResult(result), nil
}

func (s *ChunkingService) Filter(chunks []knowledgeport.TextChunk) []knowledgeport.TextChunk {
	converted := make([]textchunk.TextChunk, len(chunks))
	for i, chunk := range chunks {
		converted[i] = textchunk.TextChunk{Content: chunk.Content, ParentID: chunk.ParentID}
	}
	return fromTextChunks(s.cleaner.FilterChunks(converted))
}

func fromTextChunkResult(result textchunk.ChunkResult) knowledgeport.ChunkResult {
	return knowledgeport.ChunkResult{Leaves: fromTextChunks(result.Leaves), Parents: fromTextChunks(result.Parents)}
}

func fromTextChunks(chunks []textchunk.TextChunk) []knowledgeport.TextChunk {
	result := make([]knowledgeport.TextChunk, len(chunks))
	for i, chunk := range chunks {
		result[i] = knowledgeport.TextChunk{Content: chunk.Content, ParentID: chunk.ParentID}
	}
	return result
}

var _ knowledgeport.ChunkingService = (*ChunkingService)(nil)
