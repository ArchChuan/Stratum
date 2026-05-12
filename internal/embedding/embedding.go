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
	model  string
}

func NewEmbeddingService(apiKey string, logger *zap.Logger) *EmbeddingService {
	config := openai.DefaultConfig(apiKey)
	return &EmbeddingService{
		client: openai.NewClientWithConfig(config),
		logger: logger,
		model:  "text-embedding-3-small",
	}
}

func (e *EmbeddingService) EmbedVector(ctx context.Context, text string) ([]float32, error) {
	e.logger.Debug("generating embedding", zap.Int("text_length", len(text)))
	resp, err := e.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Input: []string{text},
		Model: openai.Embedding3Small,
	})
	if err != nil {
		e.logger.Error("failed to generate embedding", zap.Error(err))
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	e.logger.Debug("embedding generated", zap.Int("vector_size", len(resp.Data[0].Embedding)))
	return resp.Data[0].Embedding, nil
}

func (e *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	e.logger.Debug("generating batch embeddings", zap.Int("batch_size", len(texts)))
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
			Model: openai.Embedding3Small,
		})
		if err != nil {
			e.logger.Error("failed to generate batch embeddings",
				zap.Int("batch_start", i), zap.Error(err))
			return nil, fmt.Errorf("failed to generate batch embeddings: %w", err)
		}
		for _, data := range resp.Data {
			allVectors = append(allVectors, data.Embedding)
		}
	}
	e.logger.Info("batch embeddings generated", zap.Int("total_vectors", len(allVectors)))
	return allVectors, nil
}

func (e *EmbeddingService) GetVectorDimension() int {
	return 1536
}

func (e *EmbeddingService) Close() error {
	e.logger.Info("closing embedding service")
	return nil
}
