package port

import "context"

// TenantSettings reads tenant-level configuration stored in the public schema.
type TenantSettings interface {
	// GetEmbedModel returns the configured embed_model for the tenant,
	// or "" if not set.
	GetEmbedModel(ctx context.Context, tenantID string) (string, error)
}
