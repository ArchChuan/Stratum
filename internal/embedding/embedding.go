package embedding

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

type EmbeddingService struct {
	client *openai.Client
	logger *zap.Logger
}

func NewEmbeddingService(apiKey string, logger *zap.Logger) *EmbeddingService {
	config := openai.DefaultConfig(apiKey)
	client := openai.NewClientWithConfig(config)
	return &EmbeddingService{
		client: client,
		logger: logger,
	}
}

func (e *EmbeddingService) EmbedVector(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.SmallEmbedding3,
	})
	if err != nil {
		e.logger.Error("failed to create embedding", zap.Error(err))
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return resp.Data[0].Embedding, nil
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
		resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
			Input: batch,
			Model: openai.SmallEmbedding3,
		})
		if err != nil {
			e.logger.Error("failed to create batch embeddings",
				zap.Int("batch_start", i),
				zap.Int("batch_end", end),
				zap.Error(err))
			return nil, fmt.Errorf("failed to create batch embeddings: %w", err)
		}

		for _, data := range resp.Data {
			allVectors = append(allVectors, data.Embedding)
		}
	}

	return allVectors, nil
}

func (e *EmbeddingService) GetVectorDimension() int {
	return 1536
}
