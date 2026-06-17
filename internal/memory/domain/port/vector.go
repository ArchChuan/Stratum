package port

import "context"

type Document struct {
	ID     string
	Vector []float32
	Meta   map[string]any
}

type Hit struct {
	ID    string
	Score float32
	Meta  map[string]any
}

type VectorIndex interface {
	Upsert(ctx context.Context, collection string, docs []Document) error
	Search(ctx context.Context, collection string, query []float32, topK int) ([]Hit, error)
	Delete(ctx context.Context, collection string, ids []string) error
}
