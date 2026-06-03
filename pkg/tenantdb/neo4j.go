package tenantdb

import (
	"context"
	"fmt"
)

// TenantLabel returns the Neo4j node label for a given base label,
// scoped to the tenant in ctx.
// Example: label="Document" → "T_acme_Document"
func TenantLabel(ctx context.Context, label string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "T_" + tc.TenantID + "_" + label, nil
}
