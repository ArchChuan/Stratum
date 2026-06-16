// Package tenantdb provides tenant database isolation and execution helpers.

package tenantdb

import (
	"context"
	"fmt"
)

// TenantCollection returns the Milvus collection name for a given kind,
// scoped to the tenant in ctx.
// Example: kind="knowledge" → "tenant_acme_knowledge"
func TenantCollection(ctx context.Context, kind string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "tenant_" + tc.TenantID + "_" + kind, nil
}

// WorkspaceCollection returns the Milvus collection name for a specific workspace,
// scoped to the tenant in ctx.
// Example: tenant=acme, workspace=demo → "tenant_acme_kb_demo"
func WorkspaceCollection(ctx context.Context, workspace string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "tenant_" + tc.TenantID + "_kb_" + workspace, nil
}
