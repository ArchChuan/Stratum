package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/wiring"
	harnesspkg "github.com/byteBuilderX/stratum/internal/platform/harness"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type readinessPingerFake struct{ err error }

func (f readinessPingerFake) Ping(context.Context) error { return f.err }

func TestWithPostgresReadinessIncludesPingFailure(t *testing.T) {
	wantErr := errors.New("postgres down")
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{"worker": nil} },
		readinessPingerFake{err: wantErr},
		func(context.Context) error { return nil },
	)
	results := check(context.Background())
	if !errors.Is(results["postgres"], wantErr) {
		t.Fatalf("postgres error = %v, want wrapped ping failure", results["postgres"])
	}
}

func TestWithPostgresReadinessIncludesInvariantFailure(t *testing.T) {
	wantErr := errors.New("default tenant schema missing")
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{} },
		readinessPingerFake{},
		func(context.Context) error { return wantErr },
	)
	results := check(context.Background())
	if !errors.Is(results["postgres"], wantErr) {
		t.Fatalf("postgres error = %v, want wrapped invariant failure", results["postgres"])
	}
}

func TestWithPostgresReadinessIsHealthyWhenPingAndInvariantPass(t *testing.T) {
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{} },
		readinessPingerFake{},
		func(context.Context) error { return nil },
	)
	if err := check(context.Background())["postgres"]; err != nil {
		t.Fatalf("postgres error = %v, want healthy", err)
	}
}

func TestWithPostgresReadinessPreservesBaseComponentsAndFailsClosedWithoutDatabase(t *testing.T) {
	workerErr := errors.New("worker down")
	check := withPostgresReadiness(
		func(context.Context) map[string]error { return map[string]error{"worker": workerErr} },
		nil,
		func(context.Context) error { t.Fatal("invariant check called without database"); return nil },
	)
	results := check(context.Background())
	if !errors.Is(results["worker"], workerErr) {
		t.Fatalf("worker error = %v, want base component preserved", results["worker"])
	}
	if results["postgres"] == nil {
		t.Fatalf("postgres error missing without database: %#v", results)
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
