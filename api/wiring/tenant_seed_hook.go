package wiring

import (
	"context"

	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// seedAfterProvision decorates a TenantSchemaProvisioner so that, after the
// base provision succeeds, seedFn runs for the newly provisioned tenant. The
// auth path (register/guest/tenant) provisions schemas through
// Platform.SchemaProvisioner; decorating it here seeds the built-in knowledge
// workspace for every new tenant without touching IAM code. seedFn is
// responsible for its own asynchrony — the wiring closure below spawns the sync
// so a registration request is never blocked by document embedding.
type seedAfterProvision struct {
	base   iamport.TenantSchemaProvisioner
	seedFn func(ctx context.Context, tenantID string)
}

func (s seedAfterProvision) ProvisionSchema(ctx context.Context, tenantID string) error {
	if err := s.base.ProvisionSchema(ctx, tenantID); err != nil {
		return err
	}
	if s.seedFn != nil {
		s.seedFn(ctx, tenantID)
	}
	return nil
}

// ActivateTenant and MarkProvisioningFailed pass through to the base provisioner:
// only the schema-provision step is decorated with the seed hook.
func (s seedAfterProvision) ActivateTenant(ctx context.Context, tenantID string) error {
	return s.base.ActivateTenant(ctx, tenantID)
}

func (s seedAfterProvision) MarkProvisioningFailed(ctx context.Context, tenantID string) error {
	return s.base.MarkProvisioningFailed(ctx, tenantID)
}
