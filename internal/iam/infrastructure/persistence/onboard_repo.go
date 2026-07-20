// Package persistence implements iam/domain/port repos with PostgreSQL.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// OnboardRepo implements port.OnboardRepo backed by PostgreSQL.
type OnboardRepo struct {
	db *pgxpool.Pool
}

// NewOnboardRepo creates an OnboardRepo backed by the given pool.
func NewOnboardRepo(db *pgxpool.Pool) *OnboardRepo {
	return &OnboardRepo{db: db}
}

// CreateTenant runs upsert-user + insert-tenant + insert-member + create-schema in one tx.
func (r *OnboardRepo) CreateTenant(ctx context.Context, in domain.CreateTenantInput) (*domain.CreateTenantResult, error) {
	tenantID := uuid.Must(uuid.NewV7()).String()
	schemaName := "tenant_" + tenantID

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("onboard_repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var userUUID string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (github_id, github_login, avatar_url)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (github_id) DO UPDATE
		   SET github_login = EXCLUDED.github_login,
		       avatar_url   = EXCLUDED.avatar_url,
		       last_login_at = now()
		 RETURNING id`,
		fmt.Sprintf("%d", in.GitHubID), in.GitHubLogin, in.AvatarURL,
	).Scan(&userUUID)
	if err != nil {
		return nil, fmt.Errorf("onboard_repo: upsert user: %w", err)
	}

	slug := in.GitHubOrg
	if slug == "" {
		slug = tenantID[:8]
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, github_org_name) VALUES ($1, $2, $3, $4)`,
		tenantID, in.Name, slug, in.GitHubOrg,
	); err != nil {
		return nil, fmt.Errorf("onboard_repo: insert tenant: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		tenantID, userUUID,
	); err != nil {
		return nil, fmt.Errorf("onboard_repo: insert tenant_member: %w", err)
	}

	if _, err = tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName)); err != nil {
		return nil, fmt.Errorf("onboard_repo: create schema: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboard_repo: commit: %w", err)
	}
	return &domain.CreateTenantResult{TenantID: tenantID, SchemaName: schemaName, UserUUID: userUUID}, nil
}

// CreateTenantForUser creates a new tenant for an existing user (no upsert).
func (r *OnboardRepo) CreateTenantForUser(ctx context.Context, userID, name string) (string, error) {
	tenantID := uuid.Must(uuid.NewV7()).String()
	schemaName := "tenant_" + tenantID

	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("onboard_repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantID, name, tenantID[:8],
	); err != nil {
		return "", fmt.Errorf("onboard_repo: insert tenant: %w", err)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		tenantID, userID,
	); err != nil {
		return "", fmt.Errorf("onboard_repo: insert tenant_member: %w", err)
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName)); err != nil {
		return "", fmt.Errorf("onboard_repo: create schema: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("onboard_repo: commit: %w", err)
	}
	return tenantID, nil
}

// GetUserTenant returns the user's first active tenant by GitHub ID.
func (r *OnboardRepo) GetUserTenant(ctx context.Context, githubID string) (string, string, bool, error) {
	var uid, tid string
	err := r.db.QueryRow(ctx,
		`SELECT u.id, COALESCE(tm.tenant_id::text, '')
		 FROM users u
		 LEFT JOIN tenant_members tm ON tm.user_id = u.id
		 WHERE u.github_id = $1
		 LIMIT 1`,
		githubID,
	).Scan(&uid, &tid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("onboard_repo: get user tenant: %w", err)
	}
	return uid, tid, true, nil
}

// GetUserTenants returns user UUID, global_role, and all their tenants.
func (r *OnboardRepo) GetUserTenants(ctx context.Context, githubID string) (string, string, []domain.TenantInfo, bool, error) {
	var uid, gr string
	err := r.db.QueryRow(ctx,
		`SELECT id, COALESCE(global_role, '') FROM users WHERE github_id = $1`,
		githubID,
	).Scan(&uid, &gr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, fmt.Errorf("onboard_repo: get user: %w", err)
	}

	rows, err := r.db.Query(ctx,
		`SELECT t.id, t.name, t.is_default, tm.role, t.created_at
		 FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.deleted_at IS NULL
		 ORDER BY t.is_default DESC, t.created_at ASC`,
		uid,
	)
	if err != nil {
		return "", "", nil, false, fmt.Errorf("onboard_repo: list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []domain.TenantInfo
	for rows.Next() {
		var ti domain.TenantInfo
		if err := rows.Scan(&ti.TenantID, &ti.Name, &ti.IsDefault, &ti.Role, &ti.CreatedAt); err != nil {
			return "", "", nil, false, fmt.Errorf("onboard_repo: scan tenant: %w", err)
		}
		tenants = append(tenants, ti)
	}
	return uid, gr, tenants, true, nil
}

// SetGlobalRole updates users.global_role.
func (r *OnboardRepo) SetGlobalRole(ctx context.Context, userID, role string) error {
	if _, err := r.db.Exec(ctx,
		`UPDATE users SET global_role = $1 WHERE id = $2`, role, userID,
	); err != nil {
		return fmt.Errorf("onboard_repo: set global role: %w", err)
	}
	return nil
}

// GetGlobalRole returns users.global_role.
func (r *OnboardRepo) GetGlobalRole(ctx context.Context, userID string) (string, error) {
	var role string
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(global_role, '') FROM users WHERE id = $1`, userID,
	).Scan(&role); err != nil {
		return "", fmt.Errorf("onboard_repo: get global role: %w", err)
	}
	return role, nil
}

// AutoJoinDefaultTenant upserts the GitHub user and joins the default tenant.
func (r *OnboardRepo) AutoJoinDefaultTenant(ctx context.Context, in domain.AutoJoinInput) (string, string, string, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("onboard_repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var uid, gr string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (github_id, github_login, avatar_url)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (github_id) DO UPDATE
		   SET github_login  = EXCLUDED.github_login,
		       avatar_url    = EXCLUDED.avatar_url,
		       last_login_at = now()
		 RETURNING id, COALESCE(global_role, '')`,
		fmt.Sprintf("%d", in.GitHubID), in.GitHubLogin, in.AvatarURL,
	).Scan(&uid, &gr)
	if err != nil {
		return "", "", "", fmt.Errorf("onboard_repo: upsert user: %w", err)
	}

	var tid string
	if err = tx.QueryRow(ctx,
		`SELECT id FROM tenants WHERE is_default = true LIMIT 1`,
	).Scan(&tid); err != nil {
		return "", "", "", fmt.Errorf("onboard_repo: default tenant not found: %w", err)
	}

	memberRole := "member"
	if in.GlobalAdminLogin != "" && strings.EqualFold(in.GitHubLogin, in.GlobalAdminLogin) {
		memberRole = "owner"
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = $3`,
		tid, uid, memberRole,
	); err != nil {
		return "", "", "", fmt.Errorf("onboard_repo: join default tenant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", fmt.Errorf("onboard_repo: commit: %w", err)
	}
	return uid, tid, gr, nil
}

// GetTenantRole returns the role for (userID, tenantID), or "member" as fallback on error.
func (r *OnboardRepo) GetTenantRole(ctx context.Context, userID, tenantID string) (string, error) {
	var role string
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(role, 'member') FROM tenant_members WHERE user_id = $1 AND tenant_id = $2`,
		userID, tenantID,
	).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrMemberNotFound
		}
		return "member", fmt.Errorf("onboard_repo: get tenant role: %w", err)
	}
	return role, nil
}

// IsMember reports whether userID is an active member of tenantID.
func (r *OnboardRepo) IsMember(ctx context.Context, userID, tenantID string) (bool, error) {
	var count int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`,
		userID, tenantID,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("onboard_repo: is member: %w", err)
	}
	return count > 0, nil
}

// CreateGuestInDefaultTenant inserts a synthetic guest user and joins the default
// tenant as a member in one tx. Mirrors AutoJoinDefaultTenant but flags is_guest
// and stamps expires_at; caller supplies the pre-namespaced github_id.
func (r *OnboardRepo) CreateGuestInDefaultTenant(ctx context.Context, githubID, githubLogin, avatarURL string, expiresAt time.Time) (string, string, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", fmt.Errorf("onboard_repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var uid string
	err = tx.QueryRow(ctx,
		`INSERT INTO users (github_id, github_login, avatar_url, is_guest, expires_at, last_login_at)
		 VALUES ($1, $2, $3, true, $4, now())
		 RETURNING id`,
		githubID, githubLogin, avatarURL, expiresAt,
	).Scan(&uid)
	if err != nil {
		return "", "", fmt.Errorf("onboard_repo: insert guest user: %w", err)
	}

	var tid string
	if err = tx.QueryRow(ctx,
		`SELECT id FROM tenants WHERE is_default = true LIMIT 1`,
	).Scan(&tid); err != nil {
		return "", "", fmt.Errorf("onboard_repo: find default tenant: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, 'member')
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tid, uid,
	); err != nil {
		return "", "", fmt.Errorf("onboard_repo: join default tenant: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("onboard_repo: commit: %w", err)
	}
	return uid, tid, nil
}

// ListExpiredGuests returns UUIDs of guest users whose expires_at is in the past.
func (r *OnboardRepo) ListExpiredGuests(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM users WHERE is_guest = true AND expires_at IS NOT NULL AND expires_at <= $1`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard_repo: list expired guests: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("onboard_repo: scan guest id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListOwnedNonDefaultTenants returns tenant IDs the user owns that are not the default tenant.
func (r *OnboardRepo) ListOwnedNonDefaultTenants(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT t.id FROM tenants t
		 JOIN tenant_members tm ON tm.tenant_id = t.id
		 WHERE tm.user_id = $1 AND tm.role = 'owner' AND t.is_default = false AND t.deleted_at IS NULL`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard_repo: list owned tenants: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("onboard_repo: scan tenant id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteUser hard-deletes the user row; FK cascades remove tenant_members and refresh_tokens.
func (r *OnboardRepo) DeleteUser(ctx context.Context, userID string) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("onboard_repo: delete user: %w", err)
	}
	return nil
}
