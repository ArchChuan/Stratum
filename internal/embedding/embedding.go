// Package embedding provides text embedding and vectorization.
package embedding

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

type EmbeddingService struct {
	client llmgateway.EmbeddingClient
	model  string
	logger *zap.Logger
}

func NewEmbeddingService(client llmgateway.EmbeddingClient, logger *zap.Logger) *EmbeddingService {
	return &EmbeddingService{
		client: client,
		model:  "text-embedding-3-small",
		logger: logger,
	}
}

func (e *EmbeddingService) EmbedVector(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.client.CreateEmbeddings(ctx, &llmgateway.EmbeddingRequest{
		Input: []string{text},
		Model: e.model,
	})
	if err != nil {
		e.logger.Error("failed to create embedding", zap.Error(err))
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return resp.Embeddings[0], nil
}

func (e *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	const batchSize = 100
	var allVectors [][]float32

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch := texts[i:end]
		resp, err := e.client.CreateEmbeddings(ctx, &llmgateway.EmbeddingRequest{
			Input: batch,
			Model: e.model,
		})
		if err != nil {
			e.logger.Error("failed to create batch embeddings",
				zap.Int("batch_start", i),
				zap.Int("batch_end", end),
				zap.Error(err))
			return nil, fmt.Errorf("failed to create batch embeddings: %w", err)
		}

		allVectors = append(allVectors, resp.Embeddings...)
	}

	return allVectors, nil
}

func (e *EmbeddingService) GetVectorDimension() int {
	return 1536
}
