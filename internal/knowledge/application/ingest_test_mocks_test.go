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

func (m *mockEmbedder) Model() string { return "text-embedding-v3" }

// mockDocRepo satisfies knowledgeport.DocRepo for tests. All methods record
// invocations under a mutex so assertions can inspect them from the main
// goroutine even when the ingest job runs in the background.
type mockDocRepo struct {
	mu sync.Mutex

	saved        []*domain.Document
	saveErr      error
	saveInserted bool // false simulates the cross-instance admission race (INSERT conflict)
	markStarted  []struct {
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
	markFailedCtxErr error

	existsHash    map[string]bool
	existsHashErr error

	recovered    int
	recoveredErr error
	stuckWait    time.Duration
}

var _ knowledgeport.DocRepo = (*mockDocRepo)(nil)

func newMockDocRepo() *mockDocRepo {
	return &mockDocRepo{existsHash: map[string]bool{}, saveInserted: true}
}

func (m *mockDocRepo) Save(_ context.Context, _, _ string, doc *domain.Document) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return false, m.saveErr
	}
	if !m.saveInserted {
		return false, nil
	}
	m.saved = append(m.saved, doc)
	return true, nil
}

func (m *mockDocRepo) List(_ context.Context, _, _ string) ([]*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.Document, len(m.saved))
	copy(out, m.saved)
	return out, nil
}

func (m *mockDocRepo) Delete(_ context.Context, _, _, _ string) error { return nil }

// VisibleDocIDs / GetByID / SetDocAccess are not exercised by ingest tests;
// return safe defaults so the mock keeps satisfying the expanded port.
func (m *mockDocRepo) VisibleDocIDs(context.Context, string, string, string, string) ([]string, error) {
	return nil, nil
}
func (m *mockDocRepo) GetByID(context.Context, string, string, string) (*domain.Document, error) {
	return nil, domain.ErrDocumentNotFound
}
func (m *mockDocRepo) SetDocAccess(context.Context, string, string, []string, []string) error {
	return nil
}

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

func (m *mockDocRepo) MarkIngestFailed(ctx context.Context, _, docID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markFailedCtxErr = ctx.Err()
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

// CASReplace / CASBeginDelete / MarkBuiltinLegacy are not exercised by the
// ingest tests (they only cover the insert path); return winning defaults so
// the mock keeps satisfying the expanded port. The builtin-sync tests use a
// dedicated fake with controllable results instead.
func (m *mockDocRepo) CASReplace(_ context.Context, _, _, _, _, _, _ string, _ map[string]any, _ int) (bool, error) {
	return true, nil
}
func (m *mockDocRepo) CASBeginDelete(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}
func (m *mockDocRepo) MarkBuiltinLegacy(_ context.Context, _, _ string, _ []string) error {
	return nil
}

// snapshot helpers copy state under the mutex to avoid races.
func (m *mockDocRepo) savedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.saved)
}

func (m *mockDocRepo) markStartedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.markStarted)
}

func (m *mockDocRepo) markCompletedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.markCompleted)
}

func (m *mockDocRepo) markFailedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.markFailed)
}
