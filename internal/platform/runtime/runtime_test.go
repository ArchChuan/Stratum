package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

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
