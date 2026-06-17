package port

import "context"

// Embedder is the consumer-side port that knowledge/application requires for
// vectorization. The concrete implementation lives in
// internal/llmgateway/infrastructure/embedding and is bound by api/wiring.
type Embedder interface {
	EmbedVector(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	GetVectorDimension() int
}
