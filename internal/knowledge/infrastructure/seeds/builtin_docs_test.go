package seeds

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/stretchr/testify/require"
)

// stubCatalog fakes knowledgeport.OfficialDocsCatalog so seeds tests stay
// inside the knowledge context.
type stubCatalog struct {
	entries []knowledgeport.OfficialDocEntry
	err     error
}

func (s stubCatalog) AllCatalogEntries() ([]knowledgeport.OfficialDocEntry, error) {
	return s.entries, s.err
}

func testCatalog() stubCatalog {
	return stubCatalog{entries: []knowledgeport.OfficialDocEntry{
		{DocumentID: "getting-started", Title: "Getting Started", Section: "Install", Body: "install it"},
		{DocumentID: "faq", Title: "FAQ", Section: "General", Body: "answers"},
	}}
}

// --- pure helpers ---

func TestSectionSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "ascii kept", in: "Getting-Started_2", want: "Getting-Started_2"},
		{name: "spaces become underscore", in: "getting started", want: "getting_started"},
		{name: "unicode becomes underscore", in: "中文 section", want: "___section"}, // per rune, not byte
		{name: "punctuation becomes underscore", in: "a.b/c:d", want: "a_b_c_d"},
		{name: "empty stays empty", in: "", want: ""},
		{name: "mixed runes", in: "A-1_中文!", want: "A-1____"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sectionSlug(tc.in))
		})
	}
}

func TestFormatDocContent(t *testing.T) {
	entry := knowledgeport.OfficialDocEntry{Title: "T", Section: "S", Body: "B"}
	require.Equal(t, "# T\n\n## S\n\nB", formatDocContent(entry))
}

func TestContentHash_deterministicAndDistinct(t *testing.T) {
	h1 := contentHash("same content")
	h2 := contentHash("same content")
	require.Equal(t, h1, h2)
	require.Len(t, h1, 64)
	require.NotEqual(t, h1, contentHash("different content"))
	// Empty content still hashes deterministically.
	require.Equal(t, contentHash(""), contentHash(""))
}

func TestBuiltinDocID_deterministicUUIDv5(t *testing.T) {
	entry := knowledgeport.OfficialDocEntry{DocumentID: "getting-started", Section: "Install & Setup"}
	id := builtinDocID(entry)
	parsed, err := uuid.Parse(id)
	require.NoError(t, err, "docID %q must be a valid UUID (knowledge_docs.id is a UUID column)", id)
	// google/uuid v1.6.0 未导出命名版本常量；NewSHA1 按构造固定生成版本 5。
	require.Equal(t, uuid.Version(5), parsed.Version())
	// 幂等去重依赖确定性：同一条目必须始终映射到同一 doc。
	require.Equal(t, id, builtinDocID(entry))
	// 不同 section 派生不同 doc，避免跨文档内容串写。
	require.NotEqual(t, id, builtinDocID(knowledgeport.OfficialDocEntry{DocumentID: "getting-started", Section: "Other"}))
	require.NotEqual(t, id, builtinDocID(knowledgeport.OfficialDocEntry{DocumentID: "faq", Section: "Install & Setup"}))
}

// --- SeedBuiltinDocs branch coverage ---

func TestSeedBuiltinDocs_nilIngest(t *testing.T) {
	n := SeedBuiltinDocs(context.Background(), "t1", "", nil, nil, nil, nil, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_nilDocRepo(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(nil, nil, nil, nil, zap.NewNop())
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, nil, testCatalog(), nil, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_catalogFails(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(nil, nil, nil, nil, zap.NewNop())
	docRepo := newStubDocRepo()
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo,
		stubCatalog{err: errors.New("catalog decode failed")}, nil, zap.NewNop())
	require.Zero(t, n)
}

// stubDocRepo implements knowledgeport.DocRepo; only ExistsByHash and Save are
// exercised by the seed tests. saveInserted=false simulates the cross-instance
// admission race (INSERT conflict) so the seed sees IngestResult.AlreadyExists.
type stubDocRepo struct {
	exists       bool
	existsErr    error
	saveInserted bool
	saveErr      error
	savedDocIDs  []string
}

func newStubDocRepo() *stubDocRepo { return &stubDocRepo{saveInserted: true} }

func (s *stubDocRepo) ExistsByHash(context.Context, string, string, string) (bool, error) {
	return s.exists, s.existsErr
}
func (s *stubDocRepo) Save(_ context.Context, _, _ string, doc *domain.Document) (bool, error) {
	if s.saveErr != nil {
		return false, s.saveErr
	}
	if !s.saveInserted {
		return false, nil
	}
	s.savedDocIDs = append(s.savedDocIDs, doc.ID)
	return true, nil
}
func (s *stubDocRepo) List(context.Context, string, string) ([]*domain.Document, error) {
	return nil, nil
}
func (s *stubDocRepo) Delete(context.Context, string, string, string) error           { return nil }
func (s *stubDocRepo) CountByWorkspace(context.Context, string, string) (int, error)  { return 0, nil }
func (s *stubDocRepo) MarkIngestStarted(context.Context, string, string, int) error   { return nil }
func (s *stubDocRepo) MarkIngestCompleted(context.Context, string, string, int) error { return nil }
func (s *stubDocRepo) MarkIngestFailed(context.Context, string, string, string) error { return nil }
func (s *stubDocRepo) RecoverStuckIngests(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}
func (s *stubDocRepo) VisibleDocIDs(context.Context, string, string, string, string) ([]string, error) {
	return nil, nil
}
func (s *stubDocRepo) GetByID(context.Context, string, string, string) (*domain.Document, error) {
	return nil, domain.ErrDocumentNotFound
}
func (s *stubDocRepo) SetDocAccess(context.Context, string, string, []string, []string) error {
	return nil
}

// errParser fails on every document so IngestDocument returns before spawning
// the background job; seeds must log-and-continue, never block startup.
type errParser struct{}

func (errParser) ParseBytes([]byte, string) (string, error) { return "", errors.New("parse boom") }

func TestSeedBuiltinDocs_existsCheckFails(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{existsErr: errors.New("db down")}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, testCatalog(), nil, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_allSkipped(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{exists: true}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, testCatalog(), nil, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_ingestFails(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{exists: false}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, testCatalog(), nil, zap.NewNop())
	require.Zero(t, n)
}

// okParser succeeds on every document so the sync ingest path reaches Save.
type okParser struct{}

func (okParser) ParseBytes([]byte, string) (string, error) { return "install it", nil }

// stubChunker is a minimal ChunkingService: Clean/Filter are identity and
// Chunk returns a single leaf, so a seed doc reaches docRepo.Save.
type stubChunker struct{}

func (stubChunker) Clean(text string) string { return text }
func (stubChunker) Filter(chunks []knowledgeport.TextChunk) []knowledgeport.TextChunk {
	return chunks
}
func (stubChunker) Chunk(context.Context, string, string, int, int, knowledgeport.Embedder) (knowledgeport.ChunkResult, error) {
	return knowledgeport.ChunkResult{
		Leaves: []knowledgeport.TextChunk{{Content: "install it"}},
	}, nil
}

// TestSeedBuiltinDocs_alreadyExistsSkipped covers the cross-instance admission
// race: a sibling pod inserted the deterministic docID first, so our Save
// returns (false, nil) and IngestDocument reports AlreadyExists. The seed must
// count it as skipped and must NOT have spawned a pipeline (no Save insertion).
func TestSeedBuiltinDocs_alreadyExistsSkipped(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(okParser{}, stubChunker{}, nil, nil, zap.NewNop())
	docRepo := newStubDocRepo()
	docRepo.saveInserted = false // sibling pod won the race
	ingest.SetDocRepo(docRepo)   // IngestDocument's Save gate needs the repo wired
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, testCatalog(), nil, zap.NewNop())
	require.Zero(t, n, "all entries lost the admission race → nothing seeded")
	require.Len(t, docRepo.savedDocIDs, 0, "conflicted docs must not spawn a pipeline")
}

// --- ingestWithQueueRetry ---

func TestIngestWithQueueRetry_retriesOnQueueFull(t *testing.T) {
	calls := 0
	ingest := func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
		calls++
		if calls <= 2 {
			return nil, domain.ErrIngestQueueFull
		}
		return &knowledge.IngestResult{DocumentID: "doc-1", Status: constants.IngestStatusProcessing}, nil
	}
	budget := 30 * time.Second
	res, err := ingestWithQueueRetry(context.Background(), ingest, knowledge.IngestDocumentRequest{}, &budget, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, "doc-1", res.DocumentID)
	require.Equal(t, 3, calls, "two queue-full failures must be retried before success")
	// Budget spent: two waits of 500ms and 1s.
	require.Equal(t, 30*time.Second-1500*time.Millisecond, budget)
}

func TestIngestWithQueueRetry_budgetExhausted(t *testing.T) {
	ingest := func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
		return nil, domain.ErrIngestQueueFull
	}
	// Budget smaller than the base backoff: the first wait eats the whole
	// budget, then the next attempt fails immediately without waiting.
	budget := 600 * time.Millisecond
	_, err := ingestWithQueueRetry(context.Background(), ingest, knowledge.IngestDocumentRequest{}, &budget, zap.NewNop())
	require.ErrorIs(t, err, domain.ErrIngestQueueFull)
	require.Zero(t, budget, "budget must be fully consumed by the waits")
}

func TestIngestWithQueueRetry_budgetExhaustedStopsWithoutWait(t *testing.T) {
	ingest := func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
		return nil, domain.ErrIngestQueueFull
	}
	budget := time.Duration(0) // pre-exhausted by an earlier tenant
	start := time.Now()
	_, err := ingestWithQueueRetry(context.Background(), ingest, knowledge.IngestDocumentRequest{}, &budget, zap.NewNop())
	require.ErrorIs(t, err, domain.ErrIngestQueueFull)
	require.Less(t, time.Since(start), 100*time.Millisecond, "zero budget must fail fast, not wait")
}

func TestIngestWithQueueRetry_nonQueueErrorImmediate(t *testing.T) {
	sentinel := errors.New("db down")
	ingest := func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
		return nil, sentinel
	}
	budget := 30 * time.Second
	_, err := ingestWithQueueRetry(context.Background(), ingest, knowledge.IngestDocumentRequest{}, &budget, zap.NewNop())
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 30*time.Second, budget, "non-queue errors must not consume the budget")
}

func TestIngestWithQueueRetry_contextCanceled(t *testing.T) {
	ingest := func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error) {
		return nil, domain.ErrIngestQueueFull
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ingestWithQueueRetry(ctx, ingest, knowledge.IngestDocumentRequest{}, func() *time.Duration { b := 30 * time.Second; return &b }(), zap.NewNop())
	require.ErrorIs(t, err, context.Canceled)
}
