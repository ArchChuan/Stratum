package tenantnaming

import (
	"context"
	"fmt"
	"strings"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// TenantLabel returns the Neo4j node label for a given base label,
// scoped to the tenant in ctx.
// Example: label="Document" → "T_acme_Document"
func TenantLabel(ctx context.Context, label string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	return "T_" + strings.ReplaceAll(tc.TenantID, "-", "") + "_" + label, nil
}
