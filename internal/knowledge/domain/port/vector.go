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
	EnsureCollection(ctx context.Context, name string, dim int) error
	Drop(ctx context.Context, name string) error
	Upsert(ctx context.Context, collection string, docs []Document) error
	Search(ctx context.Context, collection string, query []float32, topK int) ([]Hit, error)
	Delete(ctx context.Context, collection string, ids []string) error
}
