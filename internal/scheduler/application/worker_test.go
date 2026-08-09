package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

type fakeTenantLister struct {
	tenantIDs []string
	err       error
}

func (f *fakeTenantLister) ListTenantIDs(context.Context) ([]string, error) {
	return f.tenantIDs, f.err
}

type fakeTenantPoller struct {
	mu      sync.Mutex
	calls   []string
	errs    map[string]error
	blockCh chan struct{} // 非 nil 时每个租户先阻塞等释放
}

func (f *fakeTenantPoller) PollTenant(_ context.Context, tenantID string, _ time.Time) error {
	f.mu.Lock()
	f.calls = append(f.calls, tenantID)
	err := f.errs[tenantID]
	f.mu.Unlock()
	if f.blockCh != nil {
		<-f.blockCh
	}
	return err
}

func TestWorkerPollOnceVisitsEveryTenantAndJoinsErrors(t *testing.T) {
	lister := &fakeTenantLister{tenantIDs: []string{"t1", "t2", "t3"}}
	poller := &fakeTenantPoller{errs: map[string]error{
		"t2": errors.New("tenant t2 failed"),
	}}
	w := NewWorker(lister, poller, time.Hour, observability.NoopMetrics{})

	err := w.PollOnce(context.Background())
	require.ErrorContains(t, err, "tenant t2 failed")
	require.Equal(t, []string{"t1", "t2", "t3"}, poller.calls)
	require.False(t, w.LastPollAt().IsZero(), "LastPollAt must update after a pass")
}

func TestWorkerPollOncePropagatesListerFailure(t *testing.T) {
	lister := &fakeTenantLister{err: errors.New("listing tenants failed")}
	w := NewWorker(lister, &fakeTenantPoller{}, time.Hour, observability.NoopMetrics{})

	err := w.PollOnce(context.Background())
	require.ErrorContains(t, err, "listing tenants failed")
	require.True(t, w.LastPollAt().IsZero(), "failed pass must not stamp LastPollAt")
}

func TestWorkerStartStopWaitsForInFlightPoll(t *testing.T) {
	lister := &fakeTenantLister{tenantIDs: []string{"t1"}}
	poller := &fakeTenantPoller{blockCh: make(chan struct{})}
	w := NewWorker(lister, poller, 10*time.Millisecond, observability.NoopMetrics{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// 等到 poll 进入阻塞状态（in-flight），再 Stop：Stop 必须等它完成。
	waitUntil := func(cond func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("timed out waiting for poll to start")
	}
	waitUntil(func() bool {
		poller.mu.Lock()
		defer poller.mu.Unlock()
		return len(poller.calls) >= 1
	})

	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop must wait for the in-flight poll to finish")
	case <-time.After(100 * time.Millisecond):
	}

	close(poller.blockCh)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop must return after the in-flight poll completes")
	}

	// 第二次 Stop 是 no-op，不 panic。
	w.Stop()
}

func TestWorkerPanicInPollDoesNotKillWorker(t *testing.T) {
	lister := &fakeTenantLister{tenantIDs: []string{"t1"}}
	poller := &panicPoller{}
	w := NewWorker(lister, poller, 5*time.Millisecond, observability.NoopMetrics{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && poller.pollCount() < 3 {
		time.Sleep(10 * time.Millisecond)
	}
	require.GreaterOrEqual(t, poller.pollCount(), 3, "worker must survive panics and keep polling")
	w.Stop()
}

type panicPoller struct {
	mu    sync.Mutex
	polls int
}

func (p *panicPoller) PollTenant(context.Context, string, time.Time) error {
	p.mu.Lock()
	p.polls++
	p.mu.Unlock()
	panic("boom")
}

func (p *panicPoller) pollCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.polls
}
