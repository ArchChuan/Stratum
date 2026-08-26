package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// buildIngest wires KnowledgeIngest with test doubles. vectorStore is left
// nil on purpose: tests that hit the goroutine must force an early failure
// (mockEmbedder.err) so control returns before any vectorStore call.
func buildIngest(t *testing.T, parser *mockParser, embed *mockEmbedder, docRepo *mockDocRepo) *KnowledgeIngest {
	t.Helper()
	logger := zap.NewNop()
	ki := NewKnowledgeIngest(parser, document.NewChunkingService(), embed, nil, logger)
	ki.SetDocRepo(docRepo)
	return ki
}

func req(name string) IngestDocumentRequest {
	return IngestDocumentRequest{
		TenantID:    "t1",
		Workspace:   "ws1",
		WorkspaceID: "wsid-1",
		DocumentID:  name,
		FileName:    name + ".txt",
		ContentHash: "hash-" + name,
	}
}

// paragraphInput builds n newline-delimited paragraphs of ~150 chars each.
// The chunker routes multi-paragraph input through ChunkByParagraphs, which
// avoids the pathological ChunkBySemanticBreaks path (isLatinChar treats
// ASCII '.' as non-latin, so sentence-pattern inputs OOM the sentence
// splitter). Each paragraph is above minChunkSize=100 so none is skipped.
func paragraphInput(n int) string {
	line := strings.Repeat("word ", 30) // 150 chars
	var b strings.Builder
	b.Grow(n * (len(line) + 1))
	for i := 0; i < n; i++ {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestIngestDocument_ChunkLimitExceeded(t *testing.T) {
	// ChunkByParagraphs packs paragraphs up to maxChunkSize=1000 chars per
	// chunk. One 1000-char paragraph per line → exactly one chunk per line.
	// Build MaxChunksPerDocument+1 lines to guarantee overflow.
	line := strings.Repeat("x", 1000)
	var b strings.Builder
	b.Grow((constants.MaxChunksPerDocument + 1) * 1001)
	for range constants.MaxChunksPerDocument + 1 {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	parser := &mockParser{out: b.String()}
	repo := newMockDocRepo()
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if !errors.Is(err, domain.ErrChunkLimitExceeded) {
		t.Fatalf("expected ErrChunkLimitExceeded, got %v", err)
	}
	if repo.savedCount() != 0 {
		t.Fatalf("expected no Save on rejection, got %d", repo.savedCount())
	}
}

func TestIngestDocument_QueueFull(t *testing.T) {
	parser := &mockParser{out: paragraphInput(5)}
	repo := newMockDocRepo()
	ki := buildIngest(t, parser, &mockEmbedder{err: errors.New("stop early"), dim: 4}, repo)

	// Fill queueSem to capacity so the next admission trips the default arm.
	for range constants.IngestQueueCapacity {
		ki.queueSem <- struct{}{}
	}
	defer func() {
		for range constants.IngestQueueCapacity {
			<-ki.queueSem
		}
	}()

	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if !errors.Is(err, domain.ErrIngestQueueFull) {
		t.Fatalf("expected ErrIngestQueueFull, got %v", err)
	}
}

func TestIngestDocument_ParserError(t *testing.T) {
	parser := &mockParser{err: errors.New("boom")}
	repo := newMockDocRepo()
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected parser error, got %v", err)
	}
}

func TestIngestDocument_ZeroChunks(t *testing.T) {
	// A short parser output that yields zero chunks after SmartChunk
	// filters against minChunkSize (100). "  " is below the floor.
	parser := &mockParser{out: "  "}
	repo := newMockDocRepo()
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if err == nil || !strings.Contains(err.Error(), "zero chunks") {
		t.Fatalf("expected zero-chunks error, got %v", err)
	}
}

func TestIngestDocument_HappyAdmissionSavesProcessing(t *testing.T) {
	// Embed returns an error so the goroutine's doEmbedAndPersist bails
	// before touching the nil vectorStore. The synchronous admission path
	// is what we assert.
	parser := &mockParser{out: paragraphInput(4)}
	repo := newMockDocRepo()
	ki := buildIngest(t, parser, &mockEmbedder{err: errors.New("embed unavailable"), dim: 4}, repo)

	res, err := ki.IngestDocument(context.Background(), req("d1"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != constants.IngestStatusProcessing {
		t.Fatalf("expected status=processing, got %q", res.Status)
	}
	if repo.savedCount() != 1 {
		t.Fatalf("expected exactly 1 Save (processing row), got %d", repo.savedCount())
	}
	if repo.saved[0].IngestStatus != constants.IngestStatusProcessing {
		t.Fatalf("expected saved status=processing, got %q", repo.saved[0].IngestStatus)
	}

	// Drain the background goroutine — it will land in markFailed via the
	// embed error path (no vectorStore touched).
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ki.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if repo.markFailedCount() != 1 {
		t.Fatalf("expected 1 MarkIngestFailed after embed error, got %d", repo.markFailedCount())
	}
}

func TestIngestDocument_SaveErrorReleasesQueueSlot(t *testing.T) {
	parser := &mockParser{out: paragraphInput(3)}
	repo := newMockDocRepo()
	repo.saveErr = errors.New("db down")
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if err == nil {
		t.Fatal("expected save error to bubble")
	}
	// queueSem must be empty — otherwise a failing DB write leaks slots.
	if len(ki.queueSem) != 0 {
		t.Fatalf("expected queueSem drained on save failure, got %d", len(ki.queueSem))
	}
}

// TestIngestDocument_saveConflictDoesNotSpawn covers the cross-instance
// admission gate: a sibling pod already owns the deterministic docID, so
// Save returns (false, nil) and IngestDocument must report AlreadyExists and
// spawn nothing. Spawning a second pipeline would re-run embed/insert on the
// same Milvus primary keys (no ON CONFLICT there) and flip terminal status
// between pods.
func TestIngestDocument_saveConflictDoesNotSpawn(t *testing.T) {
	parser := &mockParser{out: paragraphInput(4)}
	repo := newMockDocRepo()
	repo.saveInserted = false // sibling pod won the row
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	res, err := ki.IngestDocument(context.Background(), req("d1"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.AlreadyExists {
		t.Fatal("expected AlreadyExists=true on save conflict")
	}
	if res.Status != constants.IngestStatusProcessing {
		t.Fatalf("expected status=processing, got %q", res.Status)
	}
	if repo.savedCount() != 0 {
		t.Fatalf("expected no Save on conflict, got %d", repo.savedCount())
	}
	// Drain: if a job had been spawned it would markFailed (embed error path).
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ki.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if repo.markFailedCount() != 0 {
		t.Fatalf("expected no pipeline spawn on conflict, but a job marked the doc failed")
	}
	if repo.markStartedCount() != 0 {
		t.Fatalf("expected no pipeline spawn on conflict, but a job marked the doc started")
	}
}

// TestFlushWithRetry_transientThenSucceeds drives the flush retry loop directly:
// a transient (unavailable) flush error must be retried, and a successful
// retry must clear the error.
func TestFlushWithRetry_transientThenSucceeds(t *testing.T) {
	parser := &mockParser{out: paragraphInput(3)}
	repo := newMockDocRepo()
	vs := NewMockVectorStore()
	vs.SetFlushError(&knowledgeport.VectorStoreUnavailableError{Err: errors.New("milvus unavailable")}, 1)
	ki := NewKnowledgeIngest(parser, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	if err := ki.flushWithRetry(context.Background(), "kb_ws"); err != nil {
		t.Fatalf("expected retry to clear transient flush error, got %v", err)
	}
	if vs.flushCalls != 2 {
		t.Fatalf("expected 2 flush calls (1 failure + 1 success), got %d", vs.flushCalls)
	}
}

// TestFlushWithRetry_nonTransientFailsImmediately asserts non-transient flush
// errors are not retried — retrying a permanent failure just delays marking
// the document failed.
func TestFlushWithRetry_nonTransientFailsImmediately(t *testing.T) {
	parser := &mockParser{out: paragraphInput(3)}
	repo := newMockDocRepo()
	vs := NewMockVectorStore()
	vs.SetFlushError(errors.New("collection schema mismatch"), 100) // never succeeds
	ki := NewKnowledgeIngest(parser, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	err := ki.flushWithRetry(context.Background(), "kb_ws")
	if err == nil || !strings.Contains(err.Error(), "schema mismatch") {
		t.Fatalf("expected non-transient flush error to surface, got %v", err)
	}
	if vs.flushCalls != 1 {
		t.Fatalf("non-transient error must not retry; got %d flush calls", vs.flushCalls)
	}
}

// TestIngestDocument_flushRetriedThenCompleted is the integration check: a
// rate-limited flush inside the async job must be retried, and the doc must
// reach 'completed' instead of being failed on the transient error.
func TestIngestDocument_flushRetriedThenCompleted(t *testing.T) {
	parser := &mockParser{out: paragraphInput(4)}
	repo := newMockDocRepo()
	vs := NewMockVectorStore()
	vs.SetFlushError(&knowledgeport.VectorStoreUnavailableError{Err: errors.New("rate limit exceeded")}, 1)
	ki := NewKnowledgeIngest(parser, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	if _, err := ki.IngestDocument(context.Background(), req("d1")); err != nil {
		t.Fatalf("unexpected admission err: %v", err)
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ki.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if vs.flushCalls != 2 {
		t.Fatalf("expected 2 flush calls (1 transient failure + 1 success), got %d", vs.flushCalls)
	}
	if repo.markFailedCount() != 0 {
		t.Fatalf("transient flush error must not fail the doc; got %d MarkIngestFailed", repo.markFailedCount())
	}
	if repo.markCompletedCount() != 1 {
		t.Fatalf("expected 1 MarkIngestCompleted after flush retry, got %d", repo.markCompletedCount())
	}
}

func TestRecoverStuckIngests_DelegatesToRepo(t *testing.T) {
	parser := &mockParser{out: "unused"}
	repo := newMockDocRepo()
	repo.recovered = 7
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, repo)

	n, err := ki.RecoverStuckIngests(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 7 {
		t.Fatalf("expected 7 recovered, got %d", n)
	}
	if repo.stuckWait != constants.KnowledgeIngestStuckThreshold {
		t.Fatalf("expected threshold=%v, got %v", constants.KnowledgeIngestStuckThreshold, repo.stuckWait)
	}
}

func TestRecoverStuckIngests_NoRepoIsNoop(t *testing.T) {
	parser := &mockParser{out: "unused"}
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, nil)
	// Drop the repo we just wired to exercise the guard.
	ki.SetDocRepo(nil)

	n, err := ki.RecoverStuckIngests(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 recovered without repo, got %d", n)
	}
}

func TestPersistChunksPropagatesParentAndLeafFailures(t *testing.T) {
	result := knowledgeport.ChunkResult{
		Parents: []knowledgeport.TextChunk{{Content: "parent"}},
		Leaves:  []knowledgeport.TextChunk{{Content: "leaf"}},
	}
	docChunks := []knowledgeport.VectorDocument{{
		ID: "doc_chunk_0", Content: "leaf", SourceDocument: "doc", ChunkIndex: 0,
	}}

	for _, tc := range []struct {
		name string
		repo *recordingChunkRepo
	}{
		{name: "parent", repo: &recordingChunkRepo{parentErr: errors.New("parent failed")}},
		{name: "leaf", repo: &recordingChunkRepo{insertErr: errors.New("leaf failed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ki := &KnowledgeIngest{chunkRepo: tc.repo, logger: zap.NewNop()}
			err := ki.persistChunks(context.Background(), req("doc"), result, docChunks)
			if err == nil || !strings.Contains(err.Error(), "failed") {
				t.Fatalf("persistence failure did not propagate: %v", err)
			}
		})
	}
}

func TestMarkFailedDetachesFromCanceledJobContext(t *testing.T) {
	repo := newMockDocRepo()
	ki := &KnowledgeIngest{docRepo: repo, logger: zap.NewNop()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ki.markFailed(ctx, req("doc"), context.DeadlineExceeded)
	if repo.markFailedCtxErr != nil {
		t.Fatalf("terminal state write received canceled context: %v", repo.markFailedCtxErr)
	}
}

type ingestStatusMetrics struct {
	observability.NoopMetrics
	statuses []string
}

func (m *ingestStatusMetrics) IncKnowledgeIngest(status string) {
	m.statuses = append(m.statuses, status)
}

func TestRecordIngestCompletionFailureDoesNotEmitCompleted(t *testing.T) {
	repo := newMockDocRepo()
	repo.markCompletedErr = errors.New("completion state unavailable")
	metrics := &ingestStatusMetrics{}
	ki := &KnowledgeIngest{docRepo: repo, metrics: metrics, logger: zap.NewNop()}

	if ki.recordIngestCompletion(context.Background(), req("doc"), 3) {
		t.Fatal("completion state failure was reported as completed")
	}
	if len(metrics.statuses) != 1 || metrics.statuses[0] != constants.IngestStatusFailed {
		t.Fatalf("ingest metrics=%v, want only failed", metrics.statuses)
	}
	if repo.markFailedCount() != 1 {
		t.Fatalf("MarkIngestFailed calls=%d, want 1", repo.markFailedCount())
	}
}
