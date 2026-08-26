package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// builtinSyncDocRepo embeds deleteDocRepo (all no-op defaults) and overrides the
// sync-specific calls with controllable bookkeeping.
type builtinSyncDocRepo struct {
	deleteDocRepo
	existing []*domain.Document
	// deleteWins drives the delete CAS claim per docID (default true).
	deleteWins map[string]bool
	// legacyBackfill records the ids passed to MarkBuiltinLegacy.
	legacyBackfill [][]string
	legacyErr      error
}

func newBuiltinSyncDocRepo(existing []*domain.Document) *builtinSyncDocRepo {
	return &builtinSyncDocRepo{existing: existing, deleteWins: map[string]bool{}}
}

func (r *builtinSyncDocRepo) List(context.Context, string, string) ([]*domain.Document, error) {
	return r.existing, nil
}

func (r *builtinSyncDocRepo) CASBeginDelete(_ context.Context, _, _, docID string) (bool, error) {
	return r.deleteWins[docID], nil
}

func (r *builtinSyncDocRepo) MarkBuiltinLegacy(_ context.Context, _, _ string, ids []string) error {
	r.legacyBackfill = append(r.legacyBackfill, append([]string(nil), ids...))
	return r.legacyErr
}

// purgeRecordingVectorStore purges built-in doc vectors through the dedicated
// delete-by-document-id leg (the shared MockVectorStore no-ops).
type purgeRecordingVectorStore struct {
	MockVectorStore
	purgeAttempts int
	purgedDocIDs  []string
	purgeErr      error
}

func (v *purgeRecordingVectorStore) DeleteByDocumentIDs(_ context.Context, _ string, docIDs []string) error {
	v.purgeAttempts++
	if v.purgeErr != nil {
		return v.purgeErr
	}
	v.purgedDocIDs = append(v.purgedDocIDs, docIDs...)
	return nil
}

// fakeBuiltinSource serves a fixed doc list to the sync engine.
type fakeBuiltinSource struct {
	docs []knowledgeport.BuiltinDoc
}

func (f *fakeBuiltinSource) AllDocs(context.Context) ([]knowledgeport.BuiltinDoc, error) {
	return f.docs, nil
}

// builtinSyncHarness wires a sync engine with an ingest stub that records every
// accepted request. The stub returns a winning (non-AlreadyExists) result unless
// overridden via opts.
func builtinSyncHarness(t *testing.T, src []knowledgeport.BuiltinDoc, existing []*domain.Document, legacyIDs []string) (*BuiltinDocsSync, *builtinSyncDocRepo, *purgeRecordingVectorStore, func() []IngestDocumentRequest, func(error)) {
	t.Helper()
	docRepo := newBuiltinSyncDocRepo(existing)
	vectors := &purgeRecordingVectorStore{}
	ingested := []IngestDocumentRequest{}
	stubErr := error(nil)
	ingest := func(_ context.Context, req IngestDocumentRequest) (*IngestResult, error) {
		if stubErr != nil {
			return nil, stubErr
		}
		ingested = append(ingested, req)
		return &IngestResult{DocumentID: req.DocumentID, Workspace: req.Workspace, Status: constants.IngestStatusProcessing}, nil
	}
	ready := func() []IngestDocumentRequest { return ingested }
	fail := func(err error) { stubErr = err }
	s := NewBuiltinDocsSync(ingest, docRepo, vectors, &fakeBuiltinSource{docs: src}, legacyIDs, zap.NewNop())
	return s, docRepo, vectors, ready, fail
}

func builtinDoc(id, path, hash string) knowledgeport.BuiltinDoc {
	return knowledgeport.BuiltinDoc{DocID: id, Path: path, Title: path, Category: "guides", Content: "content", Hash: hash}
}

func storedBuiltin(id, hash, status string) *domain.Document {
	return &domain.Document{
		ID:           id,
		KBID:         constants.BuiltinWorkspaceID,
		Source:       "guides/" + id + ".md",
		ContentHash:  hash,
		IngestStatus: status,
		Metadata:     map[string]any{"builtin_source": "guides/" + id + ".md"},
	}
}

func TestBuiltinSync_fullThreeStateDiff(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{
		builtinDoc("docA", "guides/a.md", "a2"), // stored hash a1 → update
		builtinDoc("docB", "guides/b.md", "b1"), // not stored → create
		builtinDoc("docD", "guides/d.md", "d1"), // stored, unchanged → skip
	}
	existing := []*domain.Document{
		storedBuiltin("docA", "a1", constants.IngestStatusCompleted),
		storedBuiltin("docD", "d1", constants.IngestStatusCompleted),
		storedBuiltin("docC", "c1", constants.IngestStatusCompleted), // gone from source → delete
		{ID: "user1", KBID: constants.BuiltinWorkspaceID, Source: "upload.md", ContentHash: "u1", IngestStatus: constants.IngestStatusCompleted},
	}
	s, docRepo, vectors, ingested, _ := builtinSyncHarness(t, src, existing, nil)
	docRepo.deleteWins["docC"] = true

	report := s.SyncForTenant(context.Background(), "t1", "m1", nil)

	// Skipped counts docs a sibling pod already owns (ingest AlreadyExists);
	// the unchanged D doc is simply absent from the report.
	require.Equal(t, SyncReport{Created: 1, Updated: 1, Deleted: 1, Skipped: 0}, report)
	require.Equal(t, 2, len(ingested()), "B create + A update, D untouched")
	byID := map[string]IngestDocumentRequest{}
	for _, req := range ingested() {
		byID[req.DocumentID] = req
	}
	// Create path carries no expected hash; update path CASes against the old hash.
	require.Equal(t, "", byID["docB"].ExpectedContentHash)
	require.Equal(t, "a1", byID["docA"].ExpectedContentHash)
	// Delete path: CASBeginDelete win → vector purge → row delete.
	require.Equal(t, []string{"docC"}, vectors.purgedDocIDs)
	require.Equal(t, []string{"docC"}, docRepo.deletedIDs)
}

func TestBuiltinSync_failedStatusRetriesUpdate(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{builtinDoc("docA", "guides/a.md", "a1")}
	existing := []*domain.Document{storedBuiltin("docA", "a1", constants.IngestStatusFailed)}
	s, _, _, ingested, _ := builtinSyncHarness(t, src, existing, nil)

	report := s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, 1, report.Updated)
	require.Equal(t, "a1", ingested()[0].ExpectedContentHash, "failed doc retried as CAS replace")
}

func TestBuiltinSync_deleteCAsLoseSkipsCleanup(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{}
	existing := []*domain.Document{storedBuiltin("docC", "c1", constants.IngestStatusCompleted)}
	s, docRepo, vectors, _, _ := builtinSyncHarness(t, src, existing, nil)
	docRepo.deleteWins["docC"] = false // sibling pod owns the cleanup

	report := s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, 0, report.Deleted)
	require.Equal(t, 0, len(vectors.purgedDocIDs), "lost CAS must not purge vectors")
	require.Equal(t, 0, len(docRepo.deletedIDs), "lost CAS must not delete the row")
}

func TestBuiltinSync_deletePurgeFailureLeavesDocDeleting(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{}
	existing := []*domain.Document{storedBuiltin("docC", "c1", constants.IngestStatusCompleted)}
	s, docRepo, vectors, _, _ := builtinSyncHarness(t, src, existing, nil)
	docRepo.deleteWins["docC"] = true
	vectors.purgeErr = errors.New("milvus down")

	report := s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, 0, report.Deleted)
	require.Equal(t, 0, len(docRepo.deletedIDs), "row must survive a failed vector purge")
	require.Equal(t, 1, vectors.purgeAttempts, "purge was attempted before giving up on the delete")
}

func TestBuiltinSync_budgetExhaustedDefersRemaining(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{
		builtinDoc("docA", "guides/a.md", "a1"),
		builtinDoc("docB", "guides/b.md", "b1"),
		builtinDoc("docC", "guides/c.md", "c1"),
	}
	zero := time.Duration(0)

	// First doc succeeds, the queue then fills: with a drained budget the
	// remaining docs must be deferred (not blocked, not double-counted).
	ingestSucceeds := 0
	docRepo := newBuiltinSyncDocRepo(nil)
	vectors := &purgeRecordingVectorStore{}
	ingest := func(_ context.Context, req IngestDocumentRequest) (*IngestResult, error) {
		if ingestSucceeds < 1 {
			ingestSucceeds++
			return &IngestResult{DocumentID: req.DocumentID, Workspace: req.Workspace, Status: constants.IngestStatusProcessing}, nil
		}
		return nil, domain.ErrIngestQueueFull
	}
	s := NewBuiltinDocsSync(ingest, docRepo, vectors, &fakeBuiltinSource{docs: src}, nil, zap.NewNop())

	report := s.SyncForTenant(context.Background(), "t1", "m1", &zero)

	require.Equal(t, 1, report.Created, "one create lands before the budget drains")
	require.Equal(t, 1, ingestSucceeds)
	require.Equal(t, 0, report.Skipped, "deferred docs are not counted as skips")
}

func TestBuiltinSync_legacyBackfillCalled(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{builtinDoc("docA", "guides/a.md", "a1")}
	legacy := []string{"legacy-1", "legacy-2"}
	s, docRepo, _, _, _ := builtinSyncHarness(t, src, nil, legacy)

	s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, [][]string{{"legacy-1", "legacy-2"}}, docRepo.legacyBackfill)
}

func TestBuiltinSync_legacyBackfillSkippedWhenEmpty(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{builtinDoc("docA", "guides/a.md", "a1")}
	s, docRepo, _, _, _ := builtinSyncHarness(t, src, nil, nil)

	s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, 0, len(docRepo.legacyBackfill))
}

func TestBuiltinSync_legacyBackfillErrorIsNonFatal(t *testing.T) {
	src := []knowledgeport.BuiltinDoc{builtinDoc("docA", "guides/a.md", "a1")}
	s, docRepo, _, ingested, _ := builtinSyncHarness(t, src, nil, []string{"legacy-1"})
	docRepo.legacyErr = errors.New("db down")

	report := s.SyncForTenant(context.Background(), "t1", "m1", nil)

	require.Equal(t, 1, report.Created, "sync continues after a legacy backfill failure")
	require.Equal(t, 1, len(ingested()))
}

func TestBuiltinSync_missingDependenciesSkips(t *testing.T) {
	s := NewBuiltinDocsSync(nil, nil, nil, &fakeBuiltinSource{}, nil, zap.NewNop())
	require.Equal(t, SyncReport{}, s.SyncForTenant(context.Background(), "t1", "m1", nil))
}
