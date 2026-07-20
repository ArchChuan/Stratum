package handler

import (
	"context"
	"errors"
	"testing"
)

type tenantProvisionerFake struct {
	provisionErr error
	activated    bool
	failed       bool
}

func (f *tenantProvisionerFake) ProvisionSchema(context.Context, string) error { return f.provisionErr }
func (f *tenantProvisionerFake) ActivateTenant(context.Context, string) error {
	f.activated = true
	return nil
}
func (f *tenantProvisionerFake) MarkProvisioningFailed(context.Context, string) error {
	f.failed = true
	return nil
}

func TestCompleteTenantProvisionDoesNotActivateFailure(t *testing.T) {
	provisioner := &tenantProvisionerFake{provisionErr: errors.New("ddl failed")}
	if err := completeTenantProvision(context.Background(), provisioner, "tenant-1"); err == nil {
		t.Fatal("expected provisioning failure")
	}
	if provisioner.activated {
		t.Fatal("failed tenant was activated")
	}
	if !provisioner.failed {
		t.Fatal("failed tenant state was not recorded")
	}
}
