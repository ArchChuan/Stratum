package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// TestProvisionPublicSchemaKeepsPlatformModelCatalog guards the regression where
// ProvisionPublicSchema (public_schema.sql's deprecated-table block) dropped
// public.models on every startup. Since 035_platform_model_catalog re-created
// public.models as the platform-wide model catalogue (migration-owned), the
// startup bootstrap must never drop it again.
func TestProvisionPublicSchemaKeepsPlatformModelCatalog(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Simulate migration 035 having created the platform model catalogue.
	// IF NOT EXISTS keeps this idempotent when the real catalogue already exists.
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.models (
			id                TEXT PRIMARY KEY,
			provider_id       TEXT NOT NULL,
			name              TEXT NOT NULL,
			display_name      TEXT NOT NULL DEFAULT '',
			capabilities      TEXT[] NOT NULL DEFAULT '{}',
			context_window    INT NOT NULL DEFAULT 0,
			max_tokens        INT NOT NULL DEFAULT 0,
			input_price       DOUBLE PRECISION NOT NULL DEFAULT 0,
			output_price      DOUBLE PRECISION NOT NULL DEFAULT 0,
			recommended       BOOLEAN NOT NULL DEFAULT false,
			enabled           BOOLEAN NOT NULL DEFAULT true,
			provider_managed  BOOLEAN NOT NULL DEFAULT false,
			default_embedding BOOLEAN NOT NULL DEFAULT false,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (provider_id, name)
		)`)
	require.NoError(t, err)

	// The startup bootstrap must not drop the migration-owned catalogue.
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))

	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'models')`).Scan(&exists))
	require.True(t, exists, "ProvisionPublicSchema must not drop migration-owned public.models")
}
