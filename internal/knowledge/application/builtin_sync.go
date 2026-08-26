package application

import (
	"context"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// SyncReport summarizes one SyncForTenant pass.
type SyncReport struct {
	Created int
	Updated int
	Deleted int
	Skipped int
}

// builtinUpdate pairs an incoming built-in doc with the hash of the currently
// stored version, so the replace can CAS against the old content.
type builtinUpdate struct {
	doc     knowledgeport.BuiltinDoc
	oldHash string
}

// changeSet is the classified three-state diff for one sync pass. deletes are
// stored docIDs; creates/updates are source docs (updates carry the old hash).
type changeSet struct {
	creates []knowledgeport.BuiltinDoc
	updates []builtinUpdate
	deletes []string
}

// BuiltinDocsSync reconciles the built-in platform knowledge workspace with the
// repository-embedded docs/knowledge tree. It replaces the old start-up catalog
// seed: per tenant, a three-state diff (create/update/delete/keep) so editing
// one file only re-ingests that document on the next deploy. All state
// transitions are guarded by CAS in the DocRepo so multiple pods can sync the
// same tenant concurrently without double-ingesting.
type BuiltinDocsSync struct {
	ingest    func(context.Context, IngestDocumentRequest) (*IngestResult, error)
	docRepo   knowledgeport.DocRepo
	vector    knowledgeport.VectorStore
	source    knowledgeport.BuiltinDocSource
	legacyIDs []string
	logger    *zap.Logger
}

// NewBuiltinDocsSync wires the sync engine. ingest is the KnowledgeIngest
// accept path (stubbed in tests); vector is used only by the delete path.
func NewBuiltinDocsSync(
	ingest func(context.Context, IngestDocumentRequest) (*IngestResult, error),
	docRepo knowledgeport.DocRepo,
	vector knowledgeport.VectorStore,
	source knowledgeport.BuiltinDocSource,
	legacyIDs []string,
	logger *zap.Logger,
) *BuiltinDocsSync {
	return &BuiltinDocsSync{
		ingest:    ingest,
		docRepo:   docRepo,
		vector:    vector,
		source:    source,
		legacyIDs: legacyIDs,
		logger:    logger,
	}
}

// SyncForTenant reconciles the built-in workspace for one tenant. Execution
// order is deletes → creates → updates so removed docs free space first and the
// stale snapshot window is shortest. queueRetryBudget is the shared wall budget
// for retrying a full ingest queue; nil disables retries (fail fast — the new
// tenant provision path).
func (s *BuiltinDocsSync) SyncForTenant(ctx context.Context, tenantID, embedModel string, queueRetryBudget *time.Duration) SyncReport {
	if s.ingest == nil || s.docRepo == nil || s.source == nil {
		s.logger.Debug("knowledge.builtin_sync.skipped", zap.String("reason", "ingest, docRepo or source not configured"))
		return SyncReport{}
	}
	if queueRetryBudget == nil {
		zero := time.Duration(0)
		queueRetryBudget = &zero
	}

	src, err := s.source.AllDocs(ctx)
	if err != nil {
		s.logger.Error("knowledge.builtin_sync.source_failed", zap.String("tenant_id", tenantID), zap.Error(err))
		return SyncReport{}
	}
	existing, err := s.docRepo.List(ctx, tenantID, constants.BuiltinWorkspaceID)
	if err != nil {
		s.logger.Error("knowledge.builtin_sync.list_failed", zap.String("tenant_id", tenantID), zap.Error(err))
		return SyncReport{}
	}

	// Backfill the legacy marker first so old catalog seed docs are recognized
	// as built-in and cleaned up once the new per-file docs take over. Idempotent.
	if len(s.legacyIDs) > 0 {
		if err := s.docRepo.MarkBuiltinLegacy(ctx, tenantID, constants.BuiltinWorkspaceID, s.legacyIDs); err != nil {
			s.logger.Warn("knowledge.builtin_sync.legacy_backfill_failed", zap.String("tenant_id", tenantID), zap.Error(err))
		}
	}

	changes := classifyChanges(src, existing)

	var report SyncReport
	report.Deleted = s.executeDeletes(ctx, tenantID, embedModel, changes.deletes)
	created, createdSkipped := s.executeCreates(ctx, tenantID, embedModel, changes.creates, queueRetryBudget)
	updated, updatedSkipped := s.executeUpdates(ctx, tenantID, embedModel, changes.updates, queueRetryBudget)
	report.Created = created
	report.Updated = updated
	report.Skipped = createdSkipped + updatedSkipped

	s.logger.Info("knowledge.builtin_sync.complete",
		zap.String("tenant_id", tenantID),
		zap.Int("created", report.Created),
		zap.Int("updated", report.Updated),
		zap.Int("deleted", report.Deleted),
		zap.Int("skipped", report.Skipped))
	return report
}

// classifyChanges diffs the source tree against the stored documents. A stored
// doc is "built-in" iff its metadata carries builtin_source (written by this
// engine or backfilled by MarkBuiltinLegacy) — user-uploaded docs are never
// auto-deleted. A stored built-in doc that changed hash or never completed its
// previous ingest is scheduled for update (a failed doc gets retried).
func classifyChanges(src []knowledgeport.BuiltinDoc, existing []*domain.Document) changeSet {
	srcByID := make(map[string]knowledgeport.BuiltinDoc, len(src))
	for _, d := range src {
		srcByID[d.DocID] = d
	}
	existingByID := make(map[string]*domain.Document, len(existing))
	for _, d := range existing {
		existingByID[d.ID] = d
	}

	var cs changeSet
	for _, d := range src {
		cur, ok := existingByID[d.DocID]
		if !ok {
			cs.creates = append(cs.creates, d)
			continue
		}
		if cur.ContentHash != d.Hash || cur.IngestStatus != constants.IngestStatusCompleted {
			cs.updates = append(cs.updates, builtinUpdate{doc: d, oldHash: cur.ContentHash})
		}
	}
	for _, cur := range existing {
		if _, inSource := srcByID[cur.ID]; !inSource && isBuiltinDoc(cur) {
			cs.deletes = append(cs.deletes, cur.ID)
		}
	}
	return cs
}

// isBuiltinDoc reports whether a stored document belongs to the built-in
// workspace: its metadata carries the builtin_source marker (the source path
// written by this engine, or "legacy" backfilled by MarkBuiltinLegacy).
func isBuiltinDoc(d *domain.Document) bool {
	if d.Metadata == nil {
		return false
	}
	_, ok := d.Metadata["builtin_source"]
	return ok
}

// executeDeletes runs the delete side of the sync: a one-way CAS claim, then
// vector purge + row delete. A delete that loses the CAS (doc currently
// 'processing' — a sibling pod is re-ingesting it) is skipped and retried next
// pass; a claim won but cleanup failed leaves the doc 'deleting' so the next
// pass retries it (CASBeginDelete allows deleting again).
func (s *BuiltinDocsSync) executeDeletes(ctx context.Context, tenantID, embedModel string, deletes []string) int {
	removed := 0
	for _, docID := range deletes {
		won, err := s.docRepo.CASBeginDelete(ctx, tenantID, constants.BuiltinWorkspaceID, docID)
		if err != nil {
			s.logger.Warn("knowledge.builtin_sync.delete_claim_failed",
				zap.String("tenant_id", tenantID), zap.String("document_id", docID), zap.Error(err))
			continue
		}
		if !won {
			continue // sibling pod owns the cleanup, or the doc is being re-ingested
		}
		collection := constants.CollectionName(tenantID, constants.BuiltinWorkspaceID, embedModel)
		if err := s.vector.DeleteByDocumentIDs(ctx, collection, []string{docID}); err != nil {
			s.logger.Warn("knowledge.builtin_sync.delete_vector_failed",
				zap.String("tenant_id", tenantID), zap.String("document_id", docID), zap.Error(err))
			continue
		}
		if err := s.docRepo.Delete(ctx, tenantID, constants.BuiltinWorkspaceID, docID); err != nil {
			// A row already gone concurrently is fine — the claim is done.
			if !errors.Is(err, domain.ErrDocumentNotFound) {
				s.logger.Warn("knowledge.builtin_sync.delete_row_failed",
					zap.String("tenant_id", tenantID), zap.String("document_id", docID), zap.Error(err))
				continue
			}
		}
		removed++
	}
	return removed
}

// executeCreates ingests built-in docs that do not exist in the workspace yet.
// Budget exhaustion mid-way defers the rest to the next pass. Returns
// (created, skipped); skipped counts docs a sibling pod already owns.
func (s *BuiltinDocsSync) executeCreates(
	ctx context.Context, tenantID, embedModel string, creates []knowledgeport.BuiltinDoc, budget *time.Duration,
) (int, int) {
	created, skipped := 0, 0
	for _, d := range creates {
		req := s.buildIngestRequest(tenantID, embedModel, d, "")
		res, err := ingestWithQueueRetry(ctx, s.ingest, req, budget, s.logger)
		if err != nil {
			if errors.Is(err, domain.ErrIngestQueueFull) && *budget <= 0 {
				s.logger.Warn("knowledge.builtin_sync.queue_full_budget_exhausted",
					zap.String("tenant_id", tenantID), zap.String("document_id", d.DocID))
				return created, skipped
			}
			s.logger.Warn("knowledge.builtin_sync.create_failed",
				zap.String("tenant_id", tenantID), zap.String("document_id", d.DocID), zap.Error(err))
			continue
		}
		if res.AlreadyExists {
			skipped++
			continue
		}
		created++
	}
	return created, skipped
}

// executeUpdates re-ingests built-in docs whose content changed or whose
// previous ingest never completed. The replace goes through CAS so only one pod
// wins; losers count as skipped. Returns (updated, skipped).
func (s *BuiltinDocsSync) executeUpdates(
	ctx context.Context, tenantID, embedModel string, updates []builtinUpdate, budget *time.Duration,
) (int, int) {
	updated, skipped := 0, 0
	for _, u := range updates {
		req := s.buildIngestRequest(tenantID, embedModel, u.doc, u.oldHash)
		res, err := ingestWithQueueRetry(ctx, s.ingest, req, budget, s.logger)
		if err != nil {
			if errors.Is(err, domain.ErrIngestQueueFull) && *budget <= 0 {
				s.logger.Warn("knowledge.builtin_sync.queue_full_budget_exhausted",
					zap.String("tenant_id", tenantID), zap.String("document_id", u.doc.DocID))
				return updated, skipped
			}
			s.logger.Warn("knowledge.builtin_sync.update_failed",
				zap.String("tenant_id", tenantID), zap.String("document_id", u.doc.DocID), zap.Error(err))
			continue
		}
		if res.AlreadyExists {
			skipped++
			continue
		}
		updated++
	}
	return updated, skipped
}

// buildIngestRequest assembles the ingest request for a built-in doc.
// expectedHash non-empty turns it into a CAS replace of the stored version.
func (s *BuiltinDocsSync) buildIngestRequest(tenantID, embedModel string, d knowledgeport.BuiltinDoc, expectedHash string) IngestDocumentRequest {
	return IngestDocumentRequest{
		TenantID:            tenantID,
		Workspace:           constants.BuiltinWorkspaceName,
		WorkspaceID:         constants.BuiltinWorkspaceID,
		EmbeddingModel:      embedModel,
		DocumentData:        []byte(d.Content),
		FileName:            d.Path,
		DocumentID:          d.DocID,
		ContentHash:         d.Hash,
		Title:               d.Title,
		Metadata:            map[string]any{"builtin_source": d.Path},
		ExpectedContentHash: expectedHash,
	}
}
