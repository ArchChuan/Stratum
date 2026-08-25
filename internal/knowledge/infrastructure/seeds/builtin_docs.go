// Package seeds provides idempotent seed data for built-in platform resources.
package seeds

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
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
func SeedBuiltinDocs(
	ctx context.Context,
	tenantID string,
	embedModel string,
	ingest *knowledge.KnowledgeIngest,
	docRepo knowledgeport.DocRepo,
	catalog knowledgeport.OfficialDocsCatalog,
	logger *zap.Logger,
) int {
	if ingest == nil || docRepo == nil || catalog == nil {
		logger.Debug("seed.builtin_docs.skipped", zap.String("reason", "ingest, docRepo or catalog not configured"))
		return 0
	}

	entries, err := catalog.AllCatalogEntries()
	if err != nil {
		logger.Warn("seed.builtin_docs.read_catalog_failed", zap.Error(err))
		return 0
	}

	seeded, skipped := seedCatalogEntries(ctx, tenantID, embedModel, ingest, docRepo, entries, logger)

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
// errors from individual ingest operations are logged at WARN level and do not
// block the remaining catalog. Returns the seeded and skipped counts.
func seedCatalogEntries(
	ctx context.Context,
	tenantID string,
	embedModel string,
	ingest *knowledge.KnowledgeIngest,
	docRepo knowledgeport.DocRepo,
	entries []knowledgeport.OfficialDocEntry,
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
		if _, err := ingest.IngestDocument(ctx, req); err != nil {
			logger.Warn("seed.builtin_docs.ingest_failed",
				zap.String("document_id", docID),
				zap.String("tenant_id", tenantID),
				zap.Error(err))
			continue
		}
		seeded++
	}
	return seeded, skipped
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
