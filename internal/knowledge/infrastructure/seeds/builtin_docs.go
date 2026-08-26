// Package seeds provides idempotent seed data for built-in platform resources.
package seeds

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// Seed queue-capacity retry bounds. The seed can burst 6 tenants × 43 docs
// into a 20-slot queue at startup; a full queue must be retried with backoff
// (not silently dropped) so the surge is absorbed as background workers drain
// it. The shared budget bounds total startup delay: once exhausted, remaining
// docs are deferred to the next restart (idempotent by content hash) rather
// than blocking startup on a persistently-full queue.
const (
	seedQueueRetryBaseDelay   = 500 * time.Millisecond
	seedQueueRetryMaxDelay    = 8 * time.Second
	seedQueueRetryMaxAttempts = 8
)

// BuiltinWorkspaceID is the stable UUID for the built-in stratum_docs RAG
// workspace. Must match the corresponding seed INSERT in tenant_schema.sql.
const BuiltinWorkspaceID = "a0a0a0a0-0000-0000-0000-000000000001"

// BuiltinWorkspaceName is the display name for the built-in workspace.
const BuiltinWorkspaceName = "stratum_docs"

// SeedBuiltinDocs ingests the official documentation catalog into the built-in
// RAG workspace. It is idempotent — documents that already exist (matched by
// content hash) are skipped. Errors from individual ingest operations are
// logged at WARN level and do not block startup.
//
// embedModel is the workspace-configured embedding model (e.g. "text-embedding-v3").
// When empty the ingest layer falls back to the tenant-configured default.
// catalog is injected by wiring so seeds never import a sibling context.
// queueRetryBudget is the shared wall-time budget for retrying a full ingest
// queue (shared across tenants so startup is bounded); the caller owns it and
// must pass the same pointer to every tenant so the budget is global, not
// per-tenant. Pass nil to disable queue-full retries (fail fast, current
// behavior).
func SeedBuiltinDocs(
	ctx context.Context,
	tenantID string,
	embedModel string,
	ingest *knowledge.KnowledgeIngest,
	docRepo knowledgeport.DocRepo,
	catalog knowledgeport.OfficialDocsCatalog,
	queueRetryBudget *time.Duration,
	logger *zap.Logger,
) int {
	if ingest == nil || docRepo == nil || catalog == nil {
		logger.Debug("seed.builtin_docs.skipped", zap.String("reason", "ingest, docRepo or catalog not configured"))
		return 0
	}
	if queueRetryBudget == nil {
		zero := time.Duration(0)
		queueRetryBudget = &zero
	}

	entries, err := catalog.AllCatalogEntries()
	if err != nil {
		logger.Warn("seed.builtin_docs.read_catalog_failed", zap.Error(err))
		return 0
	}

	seeded, skipped := seedCatalogEntries(ctx, tenantID, embedModel, ingest, docRepo, entries, queueRetryBudget, logger)

	if seeded > 0 || skipped > 0 {
		logger.Info("seed.builtin_docs.complete",
			zap.String("tenant_id", tenantID),
			zap.Int("seeded", seeded),
			zap.Int("skipped", skipped),
			zap.Int("total", len(entries)))
	}
	return seeded
}

// seedCatalogEntries ingests each catalog entry into the built-in RAG
// workspace. Documents already present (matched by content hash) are skipped;
// a document that lost the cross-instance admission race (IngestResult.
// AlreadyExists) is treated as skipped. Errors from individual ingest
// operations are logged at WARN level and do not block the remaining catalog.
// Returns the seeded and skipped counts.
func seedCatalogEntries(
	ctx context.Context,
	tenantID string,
	embedModel string,
	ingest *knowledge.KnowledgeIngest,
	docRepo knowledgeport.DocRepo,
	entries []knowledgeport.OfficialDocEntry,
	queueRetryBudget *time.Duration,
	logger *zap.Logger,
) (int, int) {
	seeded := 0
	skipped := 0
	for _, entry := range entries {
		content := formatDocContent(entry)
		hash := contentHash(content)
		// knowledge_docs.id is a UUID column; builtinDocID derives a deterministic
		// UUIDv5 so the ingest INSERT doesn't hit SQLSTATE 22P02 (invalid uuid
		// syntax). The readable ID is preserved in FileName → title below.
		docID := builtinDocID(entry)

		exists, err := docRepo.ExistsByHash(ctx, tenantID, BuiltinWorkspaceID, hash)
		if err != nil {
			logger.Warn("seed.builtin_docs.exists_check_failed",
				zap.String("document_id", docID),
				zap.String("tenant_id", tenantID),
				zap.Error(err))
			continue
		}
		if exists {
			skipped++
			continue
		}

		req := knowledge.IngestDocumentRequest{
			TenantID:       tenantID,
			Workspace:      BuiltinWorkspaceName,
			WorkspaceID:    BuiltinWorkspaceID,
			EmbeddingModel: embedModel,
			DocumentData:   []byte(content),
			FileName:       entry.DocumentID + ".md",
			DocumentID:     docID,
			ContentHash:    hash,
		}
		res, err := ingestWithQueueRetry(ctx, ingest.IngestDocument, req, queueRetryBudget, logger)
		if err != nil {
			// Once the shared budget is spent waiting on a persistently-full
			// queue, defer the rest to the next restart (idempotent by hash)
			// instead of hammering the queue or blocking startup.
			if errors.Is(err, domain.ErrIngestQueueFull) && *queueRetryBudget <= 0 {
				logger.Warn("seed.builtin_docs.queue_full_budget_exhausted",
					zap.String("tenant_id", tenantID),
					zap.String("document_id", docID))
				return seeded, skipped
			}
			logger.Warn("seed.builtin_docs.ingest_failed",
				zap.String("document_id", docID),
				zap.String("tenant_id", tenantID),
				zap.Error(err))
			continue
		}
		if res.AlreadyExists {
			// A sibling pod won the admission race for this deterministic docID;
			// it owns the async pipeline. Nothing more to do here.
			skipped++
			continue
		}
		seeded++
	}
	return seeded, skipped
}

// ingestWithQueueRetry calls ingest, retrying with bounded exponential backoff
// when the ingest queue is full (ErrIngestQueueFull). Each wait drains
// budgetLeft; once it hits zero the error is returned immediately so startup
// is never blocked on a persistently-full queue. Non-queue errors are returned
// as-is on the first attempt. ingest is a closure so tests can inject a stub.
func ingestWithQueueRetry(
	ctx context.Context,
	ingest func(context.Context, knowledge.IngestDocumentRequest) (*knowledge.IngestResult, error),
	req knowledge.IngestDocumentRequest,
	budgetLeft *time.Duration,
	logger *zap.Logger,
) (*knowledge.IngestResult, error) {
	backoff := seedQueueRetryBaseDelay
	for attempt := 0; attempt < seedQueueRetryMaxAttempts; attempt++ {
		res, err := ingest(ctx, req)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, domain.ErrIngestQueueFull) {
			return nil, err
		}
		if attempt >= seedQueueRetryMaxAttempts-1 || *budgetLeft <= 0 {
			return nil, err
		}
		wait := backoff
		if wait > *budgetLeft {
			wait = *budgetLeft
		}
		*budgetLeft -= wait
		logger.Debug("seed.builtin_docs.queue_full_retry",
			zap.Int("attempt", attempt+1),
			zap.Duration("wait", wait))
		if err := sleepSeedBackoff(ctx, &backoff, budgetLeft, wait); err != nil {
			return nil, err
		}
	}
	return nil, domain.ErrIngestQueueFull
}

// sleepSeedBackoff waits the given delay (interrupting on ctx cancel) and
// grows backoff up to seedQueueRetryMaxDelay for the next attempt. The budget
// drain happens in the caller so the elapsed-time accounting stays next to
// the sleep decision it mirrors.
func sleepSeedBackoff(
	ctx context.Context, backoff *time.Duration, budgetLeft *time.Duration, wait time.Duration,
) error {
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return ctx.Err()
	}
	*backoff *= 2
	if *backoff > seedQueueRetryMaxDelay {
		*backoff = seedQueueRetryMaxDelay
	}
	return nil
}

func formatDocContent(entry knowledgeport.OfficialDocEntry) string {
	return fmt.Sprintf("# %s\n\n## %s\n\n%s", entry.Title, entry.Section, entry.Body)
}

func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// sectionSlug makes a safe document-id suffix from a section header.
// Collisions across sections of the same documentId would produce the same
// docID, but catalog sections are unique within a documentId by construction.
func sectionSlug(section string) string {
	var b strings.Builder
	for _, r := range section {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z',
			r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// builtinDocID 从目录条目派生确定性的 UUIDv5。knowledge_docs.id 是 UUID 列，
// 不能直接写入人可读 documentID；以 documentID:section 对为输入，同一目录条目
// 稳定映射到同一 doc（幂等去重依赖此性质），且无需查询现有 ID。
func builtinDocID(entry knowledgeport.OfficialDocEntry) string {
	readableID := fmt.Sprintf("%s:%s", entry.DocumentID, sectionSlug(entry.Section))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(readableID)).String()
}
