package pipeline

import "context"

// EmbedClientAdapter adapts EmbedClient to memport.EmbedClient.
type EmbedClientAdapter struct{ ec EmbedClient }

func NewEmbedClientAdapter(ec EmbedClient) *EmbedClientAdapter {
	return &EmbedClientAdapter{ec: ec}
}

func (a *EmbedClientAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	return a.ec.EmbedVector(ctx, text)
}

func (a *EmbedClientAdapter) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := a.ec.EmbedVector(ctx, t)
		if err != nil {
			return nil, err
		}
		results = append(results, v)
	}
	return results, nil
}
