package port

import "context"

// TenantSchemaProvisioner runs the full tenant DDL on an existing schema.
type TenantSchemaProvisioner interface {
	ProvisionSchema(ctx context.Context, tenantID string) error
	ActivateTenant(ctx context.Context, tenantID string) error
	MarkProvisioningFailed(ctx context.Context, tenantID string) error
}
