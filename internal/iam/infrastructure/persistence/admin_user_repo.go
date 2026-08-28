package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditpersistence "github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// AdminUserRepo persists users.global_role for the platform-admin UI against
// the public schema. users is a public table, so methods use the pool directly
// (no execTenant).
type AdminUserRepo struct {
	pool pgxPool
}

// NewAdminUserRepo wires the pool.
func NewAdminUserRepo(pool pgxPool) *AdminUserRepo {
	return &AdminUserRepo{pool: pool}
}

func (r *AdminUserRepo) SearchUsers(ctx context.Context, query string, limit int) ([]port.AdminUser, error) {
	pattern := "%" + query + "%"
	rows, err := r.pool.Query(ctx,
		`SELECT u.id, COALESCE(u.username, ''), u.github_login, COALESCE(u.avatar_url, '')
		   FROM public.users u
		  WHERE u.is_guest = false
		    AND u.global_role = 'user'
		    AND (u.username ILIKE $1 OR u.github_login ILIKE $1)
		  ORDER BY u.created_at DESC LIMIT $2`,
		pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]port.AdminUser, 0)
	for rows.Next() {
		var u port.AdminUser
		if err := rows.Scan(&u.UserID, &u.Username, &u.GitHubLogin, &u.AvatarURL); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *AdminUserRepo) ListAdmins(ctx context.Context) ([]port.AdminUser, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT u.id, COALESCE(u.username, ''), u.github_login, COALESCE(u.avatar_url, ''), u.global_role
		   FROM public.users u
		  WHERE u.global_role IN ('system_admin', 'global_admin')
		    AND u.is_guest = false
		  ORDER BY u.global_role DESC, u.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]port.AdminUser, 0)
	for rows.Next() {
		var u port.AdminUser
		if err := rows.Scan(&u.UserID, &u.Username, &u.GitHubLogin, &u.AvatarURL, &u.GlobalRole); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *AdminUserRepo) SetAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("set admin role: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		"UPDATE public.users SET global_role = 'system_admin', updated_at = NOW() WHERE id = $1 AND is_guest = false",
		userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set admin role: commit: %w", err)
	}
	return nil
}

func (r *AdminUserRepo) RemoveAdminRole(ctx context.Context, userID string, actorTenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("remove admin role: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		"UPDATE public.users SET global_role = 'user', updated_at = NOW() WHERE id = $1",
		userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrUserNotFound
	}
	if err := auditpersistence.InsertPlatformAuditTx(ctx, tx, actorTenantID, audit); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("remove admin role: commit: %w", err)
	}
	return nil
}

func (r *AdminUserRepo) GetGlobalRole(ctx context.Context, userID string) (domain.GlobalRole, error) {
	var role domain.GlobalRole
	err := r.pool.QueryRow(ctx,
		"SELECT global_role FROM public.users WHERE id = $1", userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUserNotFound
	}
	return role, err
}
