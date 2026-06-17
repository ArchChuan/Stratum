// Package tenantnaming provides tenant-scoped naming helpers (pure DSL,
// no DB IO) for Milvus collections, NATS subjects, and Neo4j labels.
package tenantnaming

import (
	"context"
	"fmt"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

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
	return "tenant_" + tc.TenantID + "_" + kind, nil
}

// WorkspaceCollection returns the Milvus collection name for a specific workspace,
// scoped to the tenant in ctx.
// Example: tenant=acme, workspace=demo → "tenant_acme_kb_demo"
func WorkspaceCollection(ctx context.Context, workspace string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	return "tenant_" + tc.TenantID + "_kb_" + workspace, nil
}
