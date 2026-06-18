package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

// TenantRepo persists tenant members, invitations, and settings in PostgreSQL (public schema).
type TenantRepo struct {
	db *pgxpool.Pool
}

// NewTenantRepo creates a TenantRepo backed by the given pool.
func NewTenantRepo(db *pgxpool.Pool) *TenantRepo {
	return &TenantRepo{db: db}
}

// CountMembers returns the number of members in a tenant.
func (r *TenantRepo) CountMembers(ctx context.Context, tenantID string) (int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM public.tenant_members WHERE tenant_id=$1", tenantID,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("tenant_repo: count members: %w", err)
	}
	return total, nil
}

// ListMembers returns a page of members ordered by joined_at DESC.
func (r *TenantRepo) ListMembers(ctx context.Context, tenantID string, limit, offset int) ([]domain.Member, error) {
	rows, err := r.db.Query(ctx,
		`SELECT tm.user_id, u.github_login, u.avatar_url, tm.role, tm.joined_at
		 FROM public.tenant_members tm
		 JOIN public.users u ON u.id = tm.user_id
		 WHERE tm.tenant_id=$1
		 ORDER BY tm.joined_at DESC
		 LIMIT $2 OFFSET $3`,
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("tenant_repo: list members: %w", err)
	}
	defer rows.Close()

	var members []domain.Member
	for rows.Next() {
		var m domain.Member
		if err := rows.Scan(&m.UserID, &m.GitHubLogin, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("tenant_repo: scan member: %w", err)
		}
		members = append(members, m)
	}
	return members, nil
}

// GetMemberRole returns the role of a tenant member, or ErrMemberNotFound.
func (r *TenantRepo) GetMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	var role string
	err := r.db.QueryRow(ctx,
		"SELECT role FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrMemberNotFound
		}
		return "", fmt.Errorf("tenant_repo: get member role: %w", err)
	}
	return role, nil
}

// UpdateMemberRole updates a member's role; ErrMemberNotFound if no row matched.
func (r *TenantRepo) UpdateMemberRole(ctx context.Context, tenantID, userID, role string) error {
	tag, err := r.db.Exec(ctx,
		"UPDATE public.tenant_members SET role=$1 WHERE tenant_id=$2 AND user_id=$3",
		role, tenantID, userID)
	if err != nil {
		return fmt.Errorf("tenant_repo: update member role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMemberNotFound
	}
	return nil
}

// DeleteMember removes a member; ErrMemberNotFound if no row matched.
func (r *TenantRepo) DeleteMember(ctx context.Context, tenantID, userID string) error {
	tag, err := r.db.Exec(ctx,
		"DELETE FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID)
	if err != nil {
		return fmt.Errorf("tenant_repo: delete member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMemberNotFound
	}
	return nil
}

// CreateInvitation inserts a new invitation row.
func (r *TenantRepo) CreateInvitation(ctx context.Context, inv domain.Invitation) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO public.invitations(id, tenant_id, email, role, token_hash, expires_at, created_at, invited_by)
		 VALUES($1, $2, $3, $4, $5, $6, $7, $8)`,
		inv.ID, inv.TenantID, inv.Email, inv.Role, inv.TokenHash, inv.ExpiresAt, inv.CreatedAt, inv.InvitedBy)
	if err != nil {
		return fmt.Errorf("tenant_repo: create invitation: %w", err)
	}
	return nil
}

// GetTenantSettings returns the tenant name and raw settings JSON.
func (r *TenantRepo) GetTenantSettings(ctx context.Context, tenantID string) (string, []byte, error) {
	var name string
	var settingsJSON []byte
	err := r.db.QueryRow(ctx,
		"SELECT name, settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
	).Scan(&name, &settingsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, domain.ErrTenantNotFound
		}
		return "", nil, fmt.Errorf("tenant_repo: get tenant settings: %w", err)
	}
	return name, settingsJSON, nil
}

// UpdateTenantName changes a tenant's display name; ErrTenantNotFound on miss.
func (r *TenantRepo) UpdateTenantName(ctx context.Context, tenantID, name string) error {
	tag, err := r.db.Exec(ctx,
		"UPDATE public.tenants SET name=$1, updated_at=now() WHERE id=$2 AND deleted_at IS NULL",
		name, tenantID)
	if err != nil {
		return fmt.Errorf("tenant_repo: update tenant name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTenantNotFound
	}
	return nil
}

// UpdateTenantSettings overwrites the settings JSONB; ErrTenantNotFound on miss.
func (r *TenantRepo) UpdateTenantSettings(ctx context.Context, tenantID string, settingsJSON []byte) error {
	tag, err := r.db.Exec(ctx,
		"UPDATE public.tenants SET settings=$1, updated_at=now() WHERE id=$2 AND deleted_at IS NULL",
		settingsJSON, tenantID)
	if err != nil {
		return fmt.Errorf("tenant_repo: update tenant settings: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrTenantNotFound
	}
	return nil
}

// ListUserTenants returns all active tenants the user belongs to.
func (r *TenantRepo) ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error) {
	rows, err := r.db.Query(ctx,
		`SELECT t.id, t.name, t.is_default
		 FROM tenant_members tm
		 JOIN tenants t ON t.id = tm.tenant_id
		 WHERE tm.user_id = $1 AND t.deleted_at IS NULL
		 ORDER BY t.is_default DESC, t.created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("tenant_repo: list user tenants: %w", err)
	}
	defer rows.Close()

	var tenants []domain.UserTenantInfo
	for rows.Next() {
		var t domain.UserTenantInfo
		if err := rows.Scan(&t.TenantID, &t.Name, &t.IsDefault); err != nil {
			return nil, fmt.Errorf("tenant_repo: scan user tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, nil
}
