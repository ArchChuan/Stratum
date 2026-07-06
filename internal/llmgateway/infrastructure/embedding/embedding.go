// Package embedding provides text embedding and vectorization.
package embedding

import (
	"context"
	"fmt"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
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

// NewEmbeddingServiceWithModel creates an EmbeddingService with a specific model name.
func NewEmbeddingServiceWithModel(client llmgateway.EmbeddingClient, model string, logger *zap.Logger) *EmbeddingService {
	return &EmbeddingService{
		client: client,
		model:  model,
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

// EmbedBatch splits texts into provider-safe batches and calls the embedding
// API sequentially. Each batch is given its own context (derived from ctx) with
// LLMRequestTimeout so a slow response only aborts that batch, not the whole
// job; the outer ctx still propagates cancellation to the in-flight batch.
func (e *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	batchSize := e.client.BatchSize()
	if batchSize <= 0 {
		batchSize = 100
	}
	allVectors := make([][]float32, 0, len(texts))

	for i := 0; i < len(texts); i += batchSize {
		end := min(i+batchSize, len(texts))

		batchCtx, cancel := context.WithTimeout(ctx, constants.LLMRequestTimeout)
		resp, err := e.client.CreateEmbeddings(batchCtx, &llmgateway.EmbeddingRequest{
			Input: texts[i:end],
			Model: e.model,
		})
		cancel()
		if err != nil {
			e.logger.Error("failed to create batch embeddings",
				zap.Int("batch_start", i),
				zap.Int("batch_end", end),
				zap.Error(err))
			return nil, fmt.Errorf("batch %d-%d: %w", i, end, err)
		}

		allVectors = append(allVectors, resp.Embeddings...)
	}

	return allVectors, nil
}

func (e *EmbeddingService) GetVectorDimension() int {
	switch e.model {
	case "text-embedding-v1", "text-embedding-v2":
		return 1536 // DashScope v1/v2
	case "text-embedding-v3", "text-embedding-v4":
		return 1024 // DashScope v3/v4 default
	case "embedding-3":
		return 2048 // Zhipu
	default:
		return 1536 // OpenAI text-embedding-3-small / ada-002
	}
}
