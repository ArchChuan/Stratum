package port

import "context"

type Enricher interface {
	Summarize(ctx context.Context, text string) (string, error)
	ExtractEntities(ctx context.Context, text string) ([]string, error)
}
