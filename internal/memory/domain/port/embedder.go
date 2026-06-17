package port

import "context"

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dim() int
}
