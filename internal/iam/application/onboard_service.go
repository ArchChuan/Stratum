// Package application provides IAM application services (JWT, middleware, onboarding).
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantInfo holds basic tenant info for a user's membership.
type TenantInfo struct {
	TenantID  string
	Name      string
	IsDefault bool
	Role      string
	CreatedAt time.Time
}

// CreateTenantInput holds the fields needed to create a new tenant.
type CreateTenantInput struct {
	GitHubID    int64
	GitHubLogin string
	AvatarURL   string
	Name        string
	GitHubOrg   string
}

// CreateTenantResult is returned on successful tenant creation.
type CreateTenantResult struct {
	TenantID   string
	SchemaName string
	UserUUID   string
}

// JoinTenantInput holds the fields needed to join an existing tenant via invitation.
type JoinTenantInput struct {
	UserID          string
	InvitationToken string
}

// OnboardService handles tenant creation and joining logic.
type OnboardService struct {
	db *pgxpool.Pool
}

// NewOnboardService creates an OnboardService.
func NewOnboardService(db *pgxpool.Pool) *OnboardService {
	return &OnboardService{db: db}
}

// CreateTenant runs a transaction that:
//  1. Upserts the GitHub user into `users`, returning their UUID
//  2. Inserts a new row in `tenants`
//  3. Inserts the creator as `admin` in `tenant_members`
//  4. Executes `CREATE SCHEMA tenant_{id}`
func (s *OnboardService) CreateTenant(ctx context.Context, in CreateTenantInput) (*CreateTenantResult, error) {
	tenantID := uuid.New().String()
	schemaName := "tenant_" + tenantID

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// upsert user, get UUID
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
		return nil, fmt.Errorf("onboard: upsert user: %w", err)
	}

	slug := in.GitHubOrg
	if slug == "" {
		slug = tenantID[:8]
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, github_org_name) VALUES ($1, $2, $3, $4)`,
		tenantID, in.Name, slug, in.GitHubOrg,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		tenantID, userUUID,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	// schema name is safe: uses UUID chars (hex + hyphens) only
	_, err = tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName))
	if err != nil {
		return nil, fmt.Errorf("onboard: create schema: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboard: commit: %w", err)
	}

	return &CreateTenantResult{TenantID: tenantID, SchemaName: schemaName, UserUUID: userUUID}, nil
}

// CreateTenantForUser creates a new tenant and inserts an existing user as owner.
// Unlike CreateTenant, it does not upsert the user — the user must already exist.
func (s *OnboardService) CreateTenantForUser(ctx context.Context, userID, name string) (tenantID string, err error) {
	tenantID = uuid.New().String()
	schemaName := "tenant_" + tenantID

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)`,
		tenantID, name, tenantID[:8],
	)
	if err != nil {
		return "", fmt.Errorf("onboard: insert tenant: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
		tenantID, userID,
	)
	if err != nil {
		return "", fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	_, err = tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schemaName))
	if err != nil {
		return "", fmt.Errorf("onboard: create schema: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("onboard: commit: %w", err)
	}
	return tenantID, nil
}

// GetUserTenant looks up an existing user by GitHub ID and returns their UUID and
// first active tenant. Returns found=false if the user does not exist or has no
// tenant membership.
func (s *OnboardService) GetUserTenant(ctx context.Context, githubID string) (userID, tenantID string, found bool, err error) {
	var uid, tid string
	err = s.db.QueryRow(ctx,
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
		return "", "", false, fmt.Errorf("onboard: get user tenant: %w", err)
	}
	return uid, tid, true, nil
}

// GetUserTenants returns the user's UUID, global_role, and all tenants they belong to.
// Returns found=false if the user does not exist.
func (s *OnboardService) GetUserTenants(ctx context.Context, githubID string) (userID, globalRole string, tenants []TenantInfo, found bool, err error) {
	var uid, gr string
	err = s.db.QueryRow(ctx,
		`SELECT id, COALESCE(global_role, '') FROM users WHERE github_id = $1`,
		githubID,
	).Scan(&uid, &gr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, fmt.Errorf("onboard: get user: %w", err)
	}

	rows, err := s.db.Query(ctx,
		`SELECT t.id, t.name, t.is_default, tm.role, t.created_at
		 FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.deleted_at IS NULL
		 ORDER BY t.is_default DESC, t.created_at ASC`,
		uid,
	)
	if err != nil {
		return "", "", nil, false, fmt.Errorf("onboard: list tenants: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ti TenantInfo
		if err := rows.Scan(&ti.TenantID, &ti.Name, &ti.IsDefault, &ti.Role, &ti.CreatedAt); err != nil {
			return "", "", nil, false, fmt.Errorf("onboard: scan tenant: %w", err)
		}
		tenants = append(tenants, ti)
	}
	return uid, gr, tenants, true, nil
}

// SetGlobalRole updates the global_role field for a user by UUID.
func (s *OnboardService) SetGlobalRole(ctx context.Context, userID, role string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET global_role = $1 WHERE id = $2`,
		role, userID,
	)
	if err != nil {
		return fmt.Errorf("onboard: set global role: %w", err)
	}
	return nil
}

// GetGlobalRole returns the global_role for a user by UUID.
func (s *OnboardService) GetGlobalRole(ctx context.Context, userID string) (string, error) {
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(global_role, '') FROM users WHERE id = $1`,
		userID,
	).Scan(&role)
	if err != nil {
		return "", fmt.Errorf("onboard: get global role: %w", err)
	}
	return role, nil
}

// AutoJoinDefaultTenant upserts the GitHub user into `users` and adds them to the default tenant.
// If globalAdminLogin matches githubLogin (case-insensitive), the user is inserted as "owner";
// otherwise as "member". Returns the user UUID, default tenant ID, and the user's global_role.
func (s *OnboardService) AutoJoinDefaultTenant(ctx context.Context, githubID int64, githubLogin, avatarURL, globalAdminLogin string) (userID, tenantID, globalRole string, err error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", "", fmt.Errorf("onboard: begin tx: %w", err)
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
		fmt.Sprintf("%d", githubID), githubLogin, avatarURL,
	).Scan(&uid, &gr)
	if err != nil {
		return "", "", "", fmt.Errorf("onboard: upsert user: %w", err)
	}

	var tid string
	err = tx.QueryRow(ctx,
		`SELECT id FROM tenants WHERE is_default = true LIMIT 1`,
	).Scan(&tid)
	if err != nil {
		return "", "", "", fmt.Errorf("onboard: default tenant not found: %w", err)
	}

	memberRole := "member"
	if globalAdminLogin != "" && strings.EqualFold(githubLogin, globalAdminLogin) {
		memberRole = "owner"
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = $3`,
		tid, uid, memberRole,
	)
	if err != nil {
		return "", "", "", fmt.Errorf("onboard: join default tenant: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", "", fmt.Errorf("onboard: commit: %w", err)
	}
	return uid, tid, gr, nil
}

// GetTenantRole returns the user's role in a specific tenant, or "member" as fallback.
func (s *OnboardService) GetTenantRole(ctx context.Context, userID, tenantID string) (string, error) {
	var role string
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(role, 'member') FROM tenant_members WHERE user_id = $1 AND tenant_id = $2`,
		userID, tenantID,
	).Scan(&role)
	if err != nil {
		return "member", fmt.Errorf("onboard: get tenant role: %w", err)
	}
	return role, nil
}

// IsMember reports whether userID is an active member of tenantID.
func (s *OnboardService) IsMember(ctx context.Context, userID, tenantID string) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.id = $2 AND t.deleted_at IS NULL`,
		userID, tenantID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("onboard: is member: %w", err)
	}
	return count > 0, nil
}

// JoinTenant validates an invitation token and inserts the user into the tenant.
func (s *OnboardService) JoinTenant(ctx context.Context, in JoinTenantInput) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var tenantID, role string
	err = tx.QueryRow(ctx,
		`UPDATE invitations SET accepted_at = NOW()
		 WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > NOW()
		 RETURNING tenant_id, role`,
		in.InvitationToken,
	).Scan(&tenantID, &role)
	if err != nil {
		return fmt.Errorf("onboard: invalid or expired invitation token: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tenantID, in.UserID, role,
	)
	if err != nil {
		return fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	return tx.Commit(ctx)
}
