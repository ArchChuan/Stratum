package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// fakeProvisioner records every call and lets each method fail on demand, so
// the decorator's passthrough behaviour is observable.
type fakeProvisioner struct {
	provisioned  []string
	activated    []string
	failed       []string
	provisionErr error
}

func (f *fakeProvisioner) ProvisionSchema(_ context.Context, tenantID string) error {
	f.provisioned = append(f.provisioned, tenantID)
	return f.provisionErr
}

func (f *fakeProvisioner) ActivateTenant(_ context.Context, tenantID string) error {
	f.activated = append(f.activated, tenantID)
	return nil
}

func (f *fakeProvisioner) MarkProvisioningFailed(_ context.Context, tenantID string) error {
	f.failed = append(f.failed, tenantID)
	return nil
}

func TestSeedAfterProvision_baseSuccessRunsSeedFn(t *testing.T) {
	base := &fakeProvisioner{}
	seeded := []string{}
	hook := seedAfterProvision{
		base: base,
		seedFn: func(_ context.Context, tenantID string) {
			seeded = append(seeded, tenantID)
		},
	}

	err := hook.ProvisionSchema(context.Background(), "t-42")

	require.NoError(t, err)
	require.Equal(t, []string{"t-42"}, base.provisioned, "base provision ran first")
	require.Equal(t, []string{"t-42"}, seeded, "seed ran only after the schema was provisioned")
}

func TestSeedAfterProvision_baseFailureSkipsSeedFn(t *testing.T) {
	base := &fakeProvisioner{provisionErr: errors.New("schema DDL failed")}
	seeded := 0
	hook := seedAfterProvision{
		base: base,
		seedFn: func(_ context.Context, _ string) {
			seeded++
		},
	}

	err := hook.ProvisionSchema(context.Background(), "t-1")

	require.ErrorContains(t, err, "schema DDL failed")
	require.Equal(t, 0, seeded, "a failed provision must never trigger the seed hook")
}

func TestSeedAfterProvision_nilSeedFnIsSafe(t *testing.T) {
	base := &fakeProvisioner{}
	hook := seedAfterProvision{base: base} // seedFn nil (e.g. disabled wiring)

	err := hook.ProvisionSchema(context.Background(), "t-9")

	require.NoError(t, err)
	require.Equal(t, []string{"t-9"}, base.provisioned)
}

func TestSeedAfterProvision_passthroughMethods(t *testing.T) {
	base := &fakeProvisioner{}
	hook := seedAfterProvision{base: base}
	ctx := context.Background()

	require.NoError(t, hook.ActivateTenant(ctx, "t-7"))
	require.NoError(t, hook.MarkProvisioningFailed(ctx, "t-7"))
	require.Equal(t, []string{"t-7"}, base.activated)
	require.Equal(t, []string{"t-7"}, base.failed)
	require.Len(t, base.provisioned, 0, "passthrough methods must not re-provision")
}

var _ iamport.TenantSchemaProvisioner = seedAfterProvision{}
