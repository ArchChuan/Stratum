package application

import (
	"context"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// mockParser satisfies knowledgeport.DocumentParser for tests.
type mockParser struct {
	out string
	err error
}

func (m *mockParser) ParseBytes(_ []byte, _ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.out, nil
}

// mockEmbedder satisfies knowledgeport.Embedder for tests.
type mockEmbedder struct {
	dim int
	err error
}

func (m *mockEmbedder) EmbedVector(_ context.Context, _ string) ([]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	return make([]float32, m.dim), nil
}

func (m *mockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, m.dim)
	}
	return out, nil
}

func (m *mockEmbedder) GetVectorDimension() int { return m.dim }

// mockDocRepo satisfies knowledgeport.DocRepo for tests. All methods record
// invocations under a mutex so assertions can inspect them from the main
// goroutine even when the ingest job runs in the background.
type mockDocRepo struct {
	mu sync.Mutex

	saved       []*domain.Document
	saveErr     error
	markStarted []struct {
		ID    string
		Total int
	}
	markStartedErr error
	markCompleted  []struct {
		ID        string
		Processed int
	}
	markCompletedErr error
	markFailed       []struct{ ID, Err string }
	markFailedErr    error

	existsHash    map[string]bool
	existsHashErr error

	recovered    int
	recoveredErr error
	stuckWait    time.Duration
}

var _ knowledgeport.DocRepo = (*mockDocRepo)(nil)

func newMockDocRepo() *mockDocRepo { return &mockDocRepo{existsHash: map[string]bool{}} }

func (m *mockDocRepo) Save(_ context.Context, _, _ string, doc *domain.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, doc)
	return nil
}

func (m *mockDocRepo) List(_ context.Context, _, _ string) ([]*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.Document, len(m.saved))
	copy(out, m.saved)
	return out, nil
}

func (m *mockDocRepo) Delete(_ context.Context, _, _, _ string) error { return nil }

func (m *mockDocRepo) ExistsByHash(_ context.Context, _, _, hash string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.existsHashErr != nil {
		return false, m.existsHashErr
	}
	return m.existsHash[hash], nil
}

func (m *mockDocRepo) CountByWorkspace(_ context.Context, _, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.saved), nil
}

func (m *mockDocRepo) MarkIngestStarted(_ context.Context, _, docID string, total int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markStartedErr != nil {
		return m.markStartedErr
	}
	m.markStarted = append(m.markStarted, struct {
		ID    string
		Total int
	}{docID, total})
	return nil
}

func (m *mockDocRepo) MarkIngestCompleted(_ context.Context, _, docID string, processed int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markCompletedErr != nil {
		return m.markCompletedErr
	}
	m.markCompleted = append(m.markCompleted, struct {
		ID        string
		Processed int
	}{docID, processed})
	return nil
}

func (m *mockDocRepo) MarkIngestFailed(_ context.Context, _, docID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markFailedErr != nil {
		return m.markFailedErr
	}
	m.markFailed = append(m.markFailed, struct{ ID, Err string }{docID, errMsg})
	return nil
}

func (m *mockDocRepo) RecoverStuckIngests(_ context.Context, _ string, threshold time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stuckWait = threshold
	if m.recoveredErr != nil {
		return 0, m.recoveredErr
	}
	return m.recovered, nil
}

// snapshot helpers copy state under the mutex to avoid races.
func (m *mockDocRepo) savedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.saved)
}

func (m *mockDocRepo) markFailedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.markFailed)
}
