package application

import (
	"context"
	"testing"
	"time"
)

func TestWorkerPollOnceProcessesEachTenant(t *testing.T) {
	runner := &fakeTenantJobRunner{}
	worker := NewWorker(fakeTenantLister{ids: []string{"tenant-a", "tenant-b"}}, runner, time.Second)

	if err := worker.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(runner.tenants) != 2 || runner.tenants[0] != "tenant-a" || runner.tenants[1] != "tenant-b" {
		t.Fatalf("unexpected tenants: %#v", runner.tenants)
	}
}

type fakeTenantLister struct{ ids []string }

func (f fakeTenantLister) ListTenantIDs(context.Context) ([]string, error) { return f.ids, nil }

type fakeTenantJobRunner struct{ tenants []string }

func (f *fakeTenantJobRunner) RunOnce(_ context.Context, tenantID, _ string, _ time.Duration) (bool, error) {
	f.tenants = append(f.tenants, tenantID)
	return true, nil
}
