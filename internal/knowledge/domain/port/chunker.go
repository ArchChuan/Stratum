package port

import "github.com/byteBuilderX/stratum/internal/knowledge/domain"

type Chunker interface {
	Chunk(text string, maxChars int) []domain.Chunk
}
