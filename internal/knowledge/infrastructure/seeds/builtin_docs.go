// Package seeds provides idempotent seed data for built-in platform resources.
package seeds

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
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
func SeedBuiltinDocs(
	ctx context.Context,
	tenantID string,
	embedModel string,
	ingest *knowledge.KnowledgeIngest,
	docRepo knowledgeport.DocRepo,
	logger *zap.Logger,
) int {
	if ingest == nil || docRepo == nil {
		logger.Debug("seed.builtin_docs.skipped", zap.String("reason", "ingest or docRepo not configured"))
		return 0
	}

	entries, err := officialdocs.AllCatalogEntries()
	if err != nil {
		logger.Warn("seed.builtin_docs.read_catalog_failed", zap.Error(err))
		return 0
	}

	seeded := 0
	skipped := 0
	for _, entry := range entries {
		content := formatDocContent(entry)
		hash := contentHash(content)
		docID := fmt.Sprintf("%s:%s", entry.DocumentID, sectionSlug(entry.Section))

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

	if seeded > 0 || skipped > 0 {
		logger.Info("seed.builtin_docs.complete",
			zap.String("tenant_id", tenantID),
			zap.Int("seeded", seeded),
			zap.Int("skipped", skipped),
			zap.Int("total", len(entries)))
	}
	return seeded
}

func formatDocContent(entry officialdocs.CatalogEntry) string {
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
