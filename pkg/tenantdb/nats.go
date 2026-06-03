// Package tenantdb provides tenant database isolation and execution helpers.

package tenantdb

import (
	"context"
	"fmt"
)

// TenantSubject returns the NATS subject scoped to the tenant in ctx.
// Example: subject="exec.completed" → "tenant.acme.exec.completed"
func TenantSubject(ctx context.Context, subject string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "tenant." + tc.TenantID + "." + subject, nil
}
