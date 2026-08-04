package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/wiring"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// panicOnceOnboardRepo panics on the first ListExpiredGuests call, then succeeds.
// Used to verify that runGuestReaper recovers from tick panics and continues.
type panicOnceOnboardRepo struct {
	port.OnboardRepo // nil embed — only ListExpiredGuests is called
	panicked         atomic.Bool
	recovered        chan struct{}
}

func (r *panicOnceOnboardRepo) ListExpiredGuests(context.Context, time.Time) ([]string, error) {
	if !r.panicked.Swap(true) {
		panic("injected test panic")
	}
	select {
	case r.recovered <- struct{}{}:
	default:
	}
	return nil, nil
}

// stubAdminTenantRepo satisfies port.AdminTenantRepo with a nil embed.
// DeleteTenant is never called because ListExpiredGuests returns empty after recovery.
type stubAdminTenantRepo struct{ port.AdminTenantRepo }

// spyMetrics counts reaper panic-related metric calls.
type spyMetrics struct {
	observability.NoopMetrics
	panicCycles     atomic.Int32
	goroutinePanics atomic.Int32
}

func (m *spyMetrics) IncReaperCycle(outcome string) {
	if outcome == "panic" {
		m.panicCycles.Add(1)
	}
}

func (m *spyMetrics) IncGoroutinePanic(_ string) {
	m.goroutinePanics.Add(1)
}

func TestWithPostgresReadinessIncludesDatabaseFailure(t *testing.T) {
	wantErr := errors.New("database not ready")
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{"worker": nil} },
		func(context.Context) error { return wantErr },
	)
	results := check(context.Background())
	if !errors.Is(results["postgres"], wantErr) {
		t.Fatalf("postgres error = %v, want database readiness failure", results["postgres"])
	}
}

func TestWithPostgresReadinessIsHealthyWhenPingAndInvariantPass(t *testing.T) {
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{} },
		func(context.Context) error { return nil },
	)
	if err := check(context.Background())["postgres"]; err != nil {
		t.Fatalf("postgres error = %v, want healthy", err)
	}
}

func TestWithPostgresReadinessPreservesBaseComponents(t *testing.T) {
	workerErr := errors.New("worker down")
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{"worker": workerErr} },
		func(context.Context) error { return nil },
	)
	results := check(context.Background())
	if !errors.Is(results["worker"], workerErr) {
		t.Fatalf("worker error = %v, want base component preserved", results["worker"])
	}
}

type workflowWorkerFake struct{ started chan struct{} }

func (f *workflowWorkerFake) Run(ctx context.Context, _ time.Duration) {
	close(f.started)
	<-ctx.Done()
}

func TestWorkflowWorkerIsRegisteredAsIndependentRuntimeComponent(t *testing.T) {
	worker := &workflowWorkerFake{started: make(chan struct{})}
	container := &wiring.Container{Workflow: &wiring.Workflow{Worker: worker}}
	harness := harnesspkg.New(zap.NewNop())
	registerWorkflowWorker(harness, container, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, harness.Start(ctx))
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("workflow worker did not start")
	}
	cancel()
	require.NoError(t, harness.Stop(context.Background()))
}

func TestBootstrapTenantSchemasUsesOneProvisionLock(t *testing.T) {
	var calls []string
	deps := tenantBootstrapDeps{
		withLock: func(ctx context.Context, _ *pgxpool.Pool, fn func(context.Context) error) error {
			calls = append(calls, "lock")
			return fn(ctx)
		},
		provisionPublic: func(context.Context, *pgxpool.Pool, *zap.Logger) error {
			calls = append(calls, "public")
			return nil
		},
		ensureDefault: func(context.Context, *pgxpool.Pool, *zap.Logger) error {
			calls = append(calls, "default")
			return nil
		},
		provisionAll: func(context.Context, *pgxpool.Pool, *zap.Logger) error {
			calls = append(calls, "tenants")
			return nil
		},
	}

	if err := bootstrapTenantSchemas(context.Background(), nil, zap.NewNop(), deps); err != nil {
		t.Fatalf("bootstrapTenantSchemas: %v", err)
	}
	want := []string{"lock", "public", "default", "tenants"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestBootstrapTenantSchemasPropagatesProvisionAllFailure(t *testing.T) {
	wantErr := errors.New("tenant marker provisioning failed")
	deps := tenantBootstrapDeps{
		withLock: func(ctx context.Context, _ *pgxpool.Pool, fn func(context.Context) error) error {
			return fn(ctx)
		},
		provisionPublic: func(context.Context, *pgxpool.Pool, *zap.Logger) error { return nil },
		ensureDefault:   func(context.Context, *pgxpool.Pool, *zap.Logger) error { return nil },
		provisionAll: func(context.Context, *pgxpool.Pool, *zap.Logger) error {
			return wantErr
		},
	}

	err := bootstrapTenantSchemas(context.Background(), nil, zap.NewNop(), deps)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "tenant schemas") {
		t.Fatalf("error = %v, want wrapped provision-all failure", err)
	}
}

// TestRunGuestReaperSurvivesTickPanic verifies that the guest reaper recovers
// from a panic inside a tick body and continues running subsequent ticks. Without
// this recovery, a single panic (e.g. pgx pool exhaustion, nil deref) kills the
// goroutine silently, causing StratumReaperDown to fire after 2h.
func TestRunGuestReaperSurvivesTickPanic(t *testing.T) {
	recovered := make(chan struct{}, 5)
	onboardRepo := &panicOnceOnboardRepo{recovered: recovered}
	onboard := iamapp.NewOnboardService(onboardRepo)
	admin := iamapp.NewAdminService(&stubAdminTenantRepo{})
	metrics := &spyMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runGuestReaper(ctx, onboard, admin, metrics, 20*time.Millisecond, zap.NewNop())

	// Wait for several successful ticks — each proves the loop survived the
	// initial panic and continues firing on schedule.
	for range 3 {
		select {
		case <-recovered:
		case <-time.After(5 * time.Second):
			t.Fatal("reaper did not survive panic — timed out waiting for post-panic tick")
		}
	}

	cancel()

	if n := metrics.panicCycles.Load(); n == 0 {
		t.Error("IncReaperCycle('panic') was not called after recovery")
	}
	if n := metrics.goroutinePanics.Load(); n == 0 {
		t.Error("IncGoroutinePanic was not called after recovery")
	}
}

func TestBuildInternalRouterFailsWhenPlatformMCPIsMissing(t *testing.T) {
	container := &wiring.Container{Logger: zap.NewNop()}

	_, err := buildInternalRouter(container)

	if err == nil {
		t.Fatal("expected missing Platform MCP wiring to fail")
	}
}
