package tenantnaming

import (
	"context"
	"fmt"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// TenantSubject returns the NATS subject scoped to the tenant in ctx.
// Example: subject="exec.completed" → "tenant.acme.exec.completed"
func TenantSubject(ctx context.Context, subject string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	return "tenant." + tc.TenantID + "." + subject, nil
}
