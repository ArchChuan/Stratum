package embedding

import (
	"context"
	"fmt"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

type mockEmbeddingClient struct {
	embeddings [][]float32
	err        error
}

func (m *mockEmbeddingClient) CreateEmbeddings(_ context.Context, req *llmgateway.EmbeddingRequest) (*llmgateway.EmbeddingResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([][]float32, len(req.Input))
	for i := range req.Input {
		if i < len(m.embeddings) {
			result[i] = m.embeddings[i]
		} else {
			result[i] = []float32{0.1, 0.2, 0.3}
		}
	}
	return &llmgateway.EmbeddingResponse{Embeddings: result}, nil
}

func TestNewEmbeddingService(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{}
	service := NewEmbeddingService(mock, logger)

	if service == nil { //nolint:staticcheck
		t.Error("expected service to be non-nil")
	}
	if service.client == nil { //nolint:staticcheck
		t.Error("expected client to be non-nil")
	}
	if service.logger == nil { //nolint:staticcheck
		t.Error("expected logger to be non-nil")
	}
}

func TestEmbedVector(t *testing.T) {
	logger := zap.NewNop()
	want := []float32{0.1, 0.2, 0.3}
	mock := &mockEmbeddingClient{embeddings: [][]float32{want}}
	service := NewEmbeddingService(mock, logger)

	got, err := service.EmbedVector(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d dims, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dim %d: want %f, got %f", i, want[i], got[i])
		}
	}
}

func TestEmbedVectorError(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{err: fmt.Errorf("api error")}
	service := NewEmbeddingService(mock, logger)

	_, err := service.EmbedVector(context.Background(), "hello")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestEmbedBatch(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{}
	service := NewEmbeddingService(mock, logger)

	texts := []string{"a", "b", "c"}
	got, err := service.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(got))
	}
}

func TestGetVectorDimension(t *testing.T) {
	service := NewEmbeddingService(&mockEmbeddingClient{}, zap.NewNop())
	if service.GetVectorDimension() != 1536 {
		t.Errorf("expected 1536, got %d", service.GetVectorDimension())
	}
}
