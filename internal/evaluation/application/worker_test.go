package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestWorkerPollOnceProcessesEachTenant(t *testing.T) {
	runner := &fakeTenantJobRunner{worked: true}
	worker := NewWorker(fakeTenantLister{ids: []string{"tenant-a", "tenant-b"}}, runner, time.Second, observability.NoopMetrics{})

	worked, err := worker.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if !worked {
		t.Fatal("expected work to be reported")
	}
	if len(runner.tenants) != 2 || runner.tenants[0] != "tenant-a" || runner.tenants[1] != "tenant-b" {
		t.Fatalf("unexpected tenants: %#v", runner.tenants)
	}
}

func TestWorkerPollOnceReportsNoWork(t *testing.T) {
	runner := &fakeTenantJobRunner{}
	worker := NewWorker(fakeTenantLister{ids: []string{"tenant-a"}}, runner, time.Second, observability.NoopMetrics{})

	worked, err := worker.PollOnce(context.Background())
	if err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if worked {
		t.Fatal("expected no work when runner reports false")
	}
}

func TestWorkerIdlesWhenNoWork(t *testing.T) {
	runner := &fakeTenantJobRunner{}
	worker := NewWorker(fakeTenantLister{ids: []string{"tenant-a"}}, runner, 150*time.Millisecond, observability.NoopMetrics{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()
	time.Sleep(600 * time.Millisecond)
	cancel()
	<-done

	if got := runner.count(); got < 2 || got > 6 {
		t.Fatalf("expected 2-6 polls over 600ms with 150ms idle, got %d (no-work loop must idle, not spin)", got)
	}
}

type fakeTenantLister struct{ ids []string }

func (f fakeTenantLister) ListTenantIDs(context.Context) ([]string, error) { return f.ids, nil }

type fakeTenantJobRunner struct {
	mu      sync.Mutex
	tenants []string
	worked  bool
}

func (f *fakeTenantJobRunner) RunOnce(_ context.Context, tenantID, _ string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenants = append(f.tenants, tenantID)
	return f.worked, nil
}

func (f *fakeTenantJobRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tenants)
}
