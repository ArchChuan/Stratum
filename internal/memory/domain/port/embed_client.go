package port

import (
	"context"
)

// EmbedClient generates embeddings for text.
type EmbedClient interface {
	// Embed converts text to vector embedding.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch converts multiple texts to embeddings.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
