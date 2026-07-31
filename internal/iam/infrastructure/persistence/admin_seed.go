// Package persistence implements IAM port adapters with PostgreSQL and
// provides startup-time data seeding (admin bootstrap).
package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/crypto"
)

// EnsureAdminUser creates a global_admin user from the configured credentials
// when no admin exists yet. Idempotent — skips if an admin is already present.
// Uses schema-qualified table names because it runs at startup without tenant
// context (per migration-tenant.md: startup paths must use public.*).
func EnsureAdminUser(ctx context.Context, pool *pgxpool.Pool, username, password string, logger *zap.Logger) error {
	if username == "" || password == "" {
		return nil
	}

	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM public.users WHERE global_role = 'global_admin'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("admin seed: check existing: %w", err)
	}
	if count > 0 {
		logger.Info("admin user already exists, skipping seed")
		return nil
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return fmt.Errorf("admin seed: hash password: %w", err)
	}

	githubID := "local:" + username

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("admin seed: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var uid string
	err = tx.QueryRow(ctx,
		`INSERT INTO public.users (github_id, github_login, username, password_hash, global_role)
		 VALUES ($1, $2, $3, $4, 'global_admin')
		 ON CONFLICT (username) DO NOTHING
		 RETURNING id`,
		githubID, username, username, hash,
	).Scan(&uid)
	if err != nil {
		return fmt.Errorf("admin seed: insert admin: %w", err)
	}

	var tid string
	err = tx.QueryRow(ctx,
		`SELECT id FROM public.tenants WHERE is_default = true LIMIT 1`,
	).Scan(&tid)
	if err != nil {
		return fmt.Errorf("admin seed: find default tenant: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO public.tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, 'owner')
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = 'owner'`,
		tid, uid,
	); err != nil {
		return fmt.Errorf("admin seed: join default tenant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("admin seed: commit: %w", err)
	}

	logger.Info("admin user seeded", zap.String("username", username), zap.String("user_id", uid))
	return nil
}
