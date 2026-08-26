// Package embedding provides text embedding and vectorization.
package embedding

import (
	"context"
	"fmt"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// EmbeddingClient is the minimal embedding capability consumed by this service.
// Satisfied by *llmgateway.OpenAICompatClient and other types that implement
// CreateEmbeddings and BatchSize.
type EmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, req *llmgateway.EmbeddingRequest) (*llmgateway.EmbeddingResponse, error)
	BatchSize() int
}

type EmbeddingService struct {
	client EmbeddingClient
	model  string
	logger *zap.Logger
}

func NewEmbeddingService(client EmbeddingClient, logger *zap.Logger) *EmbeddingService {
	return &EmbeddingService{
		client: client,
		model:  "text-embedding-3-small",
		logger: logger,
	}
}

// NewEmbeddingServiceWithModel creates an EmbeddingService with a specific model name.
func NewEmbeddingServiceWithModel(client EmbeddingClient, model string, logger *zap.Logger) *EmbeddingService {
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

// defaultBatchSize 是 client 未声明批次上限时的安全兜底，与 OpenAI-compat
// 平台统一上限（64，智谱 embedding-3 官方限定单请求 ≤64 条）保持一致，
// 防止未知 provider 一次提交过多文本触发上游 400。
const defaultBatchSize = 64

// EmbedBatch splits texts into provider-safe batches and calls the embedding
// API sequentially. Each batch is given its own context (derived from ctx) with
// LLMRequestTimeout so a slow response only aborts that batch, not the whole
// job; the outer ctx still propagates cancellation to the in-flight batch.
func (e *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	batchSize := e.client.BatchSize()
	if batchSize <= 0 {
		batchSize = defaultBatchSize
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
	return constants.DimensionForModel(e.model)
}

// Model returns the embedding model name this service was built with.
func (e *EmbeddingService) Model() string { return e.model }
