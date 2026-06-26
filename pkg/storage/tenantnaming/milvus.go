// Package tenantnaming provides tenant-scoped naming helpers (pure DSL,
// no DB IO) for Milvus collections, NATS subjects, and Neo4j labels.
package tenantnaming

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

var milvusInvalidRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeWorkspace returns a Milvus-safe identifier for the workspace name.
// Pure ASCII names that are already valid are returned unchanged (backward-compat).
// Any name containing invalid chars is replaced with "h" + 16-hex-char SHA-256 prefix.
func sanitizeWorkspace(name string) string {
	if !milvusInvalidRe.MatchString(name) {
		return name
	}
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("h%x", h[:8])
}

// sanitizeTenantID replaces all Milvus-invalid chars (e.g. UUID hyphens) with underscores.
func sanitizeTenantID(id string) string {
	return milvusInvalidRe.ReplaceAllString(id, "_")
}

// TenantCollection returns the Milvus collection name for a given kind,
// scoped to the tenant in ctx.
// Example: kind="knowledge" → "tenant_acme_knowledge"
func TenantCollection(ctx context.Context, kind string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	return "tenant_" + sanitizeTenantID(tc.TenantID) + "_" + kind, nil
}

// WorkspaceCollection returns the Milvus collection name for a workspace,
// scoped to the tenant in ctx. workspaceID must be the stable workspace ID
// (not the mutable name) so renames do not change the collection name.
//
// Deprecated: prefer KnowledgeCollection + WorkspacePartition (one collection
// per tenant, one partition per workspace).
func WorkspaceCollection(ctx context.Context, workspaceID string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	return "tenant_" + sanitizeTenantID(tc.TenantID) + "_kb_" + sanitizeWorkspace(workspaceID), nil
}

// KnowledgeCollection returns the single per-tenant Milvus collection that
// holds all knowledge-base vectors. Workspaces are isolated by partition.
func KnowledgeCollection(ctx context.Context) (string, error) {
	return TenantCollection(ctx, "kb")
}

// WorkspacePartition returns a Milvus-safe partition name for the given
// workspace ID. Pure function — no context required.
func WorkspacePartition(workspaceID string) string {
	return sanitizeWorkspace(workspaceID)
}
