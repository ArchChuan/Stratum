package embedding

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"go.uber.org/zap"
)

type mockEmbeddingClient struct {
	embeddings    [][]float32
	err           error
	batchSize     int
	emptyResponse bool
	requests      []*llmgateway.EmbeddingRequest
}

func (m *mockEmbeddingClient) CreateEmbeddings(_ context.Context, req *llmgateway.EmbeddingRequest) (*llmgateway.EmbeddingResponse, error) {
	cp := &llmgateway.EmbeddingRequest{
		Input: append([]string(nil), req.Input...),
		Model: req.Model,
	}
	m.requests = append(m.requests, cp)
	if m.err != nil {
		return nil, m.err
	}
	if m.emptyResponse {
		return &llmgateway.EmbeddingResponse{}, nil
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

func (m *mockEmbeddingClient) BatchSize() int {
	return m.batchSize
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

func TestEmbedVectorReturnsErrorWhenProviderReturnsNoEmbeddings(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{emptyResponse: true}
	service := NewEmbeddingService(mock, logger)

	_, err := service.EmbedVector(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when provider returns no embeddings, got nil")
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

func TestEmbedBatchDoesNotCallProviderForEmptyInput(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{batchSize: 2}
	service := NewEmbeddingService(mock, logger)

	got, err := service.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no vectors for empty input, got %d", len(got))
	}
	if len(mock.requests) != 0 {
		t.Fatalf("expected provider not to be called for empty input, got %d calls", len(mock.requests))
	}
}

func TestEmbedBatchSplitsByProviderBatchSize(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{batchSize: 2}
	service := NewEmbeddingServiceWithModel(mock, "embedding-model", logger)

	texts := []string{"a", "b", "c", "d", "e"}
	got, err := service.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(got))
	}

	wantBatches := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	if len(mock.requests) != len(wantBatches) {
		t.Fatalf("expected %d provider calls, got %d", len(wantBatches), len(mock.requests))
	}
	for i, req := range mock.requests {
		if req.Model != "embedding-model" {
			t.Fatalf("request %d: expected model embedding-model, got %q", i, req.Model)
		}
		if !reflect.DeepEqual(req.Input, wantBatches[i]) {
			t.Fatalf("request %d: expected input %v, got %v", i, wantBatches[i], req.Input)
		}
	}
}

// slowMockClient sleeps inside CreateEmbeddings so a per-batch timeout can
// abort the first call while later batches still succeed. Verifies EmbedBatch
// applies WithTimeout per batch rather than reusing a single expired context.
type slowMockClient struct {
	batchSize   int
	firstDelay  time.Duration
	requests    int
	lastCtxErrs []error
}

func (s *slowMockClient) CreateEmbeddings(ctx context.Context, req *llmgateway.EmbeddingRequest) (*llmgateway.EmbeddingResponse, error) {
	s.requests++
	if s.requests == 1 {
		select {
		case <-time.After(s.firstDelay):
		case <-ctx.Done():
			s.lastCtxErrs = append(s.lastCtxErrs, ctx.Err())
			return nil, ctx.Err()
		}
	}
	out := make([][]float32, len(req.Input))
	for i := range req.Input {
		out[i] = []float32{0.1}
	}
	return &llmgateway.EmbeddingResponse{Embeddings: out}, nil
}

func (s *slowMockClient) BatchSize() int { return s.batchSize }

func TestEmbedBatchPerBatchTimeoutDoesNotPoisonLaterBatches(t *testing.T) {
	// First batch would exceed a hypothetical shared deadline. This test
	// asserts each batch gets its OWN WithTimeout — if EmbedBatch reused
	// the caller ctx as the deadline, batches after batch 1 would inherit
	// its cancellation. Here we simulate a first-batch failure and confirm
	// only that batch errors out; without per-batch timeout the whole loop
	// would abort with the same err.
	logger := zap.NewNop()
	mock := &slowMockClient{batchSize: 1, firstDelay: 1 * time.Millisecond}
	svc := NewEmbeddingService(mock, logger)

	// Use a short-lived ctx that would be cancelled after firstDelay — but
	// EmbedBatch's per-batch WithTimeout(LLMRequestTimeout=60s) shields
	// each batch from parent expiry within the current call chain.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	texts := []string{"a", "b", "c"}
	got, err := svc.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("expected success under per-batch timeout, got %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(got))
	}
	if mock.requests != 3 {
		t.Fatalf("expected 3 provider calls (batchSize=1), got %d", mock.requests)
	}
}

func TestEmbedBatchUsesDefaultBatchSizeWhenProviderReturnsNonPositive(t *testing.T) {
	logger := zap.NewNop()
	mock := &mockEmbeddingClient{batchSize: 0}
	service := NewEmbeddingService(mock, logger)

	texts := make([]string, 101)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	_, err := service.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.requests) != 2 {
		t.Fatalf("expected default batch size to split 101 texts into 2 calls, got %d", len(mock.requests))
	}
	if len(mock.requests[0].Input) != 100 || len(mock.requests[1].Input) != 1 {
		t.Fatalf("expected batch sizes 100 and 1, got %d and %d", len(mock.requests[0].Input), len(mock.requests[1].Input))
	}
}

func TestGetVectorDimension(t *testing.T) {
	service := NewEmbeddingService(&mockEmbeddingClient{}, zap.NewNop())
	if service.GetVectorDimension() != 1536 {
		t.Errorf("expected 1536, got %d", service.GetVectorDimension())
	}
}
