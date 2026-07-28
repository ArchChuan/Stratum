package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/jackc/pgx/v5"
)

type InvitationRepo struct {
	db pgxPool
}

func NewInvitationRepo(db pgxPool) *InvitationRepo {
	return &InvitationRepo{db: db}
}

func (r *InvitationRepo) Create(ctx context.Context, invitation domain.TenantInvitation) error {
	_, err := r.db.Exec(ctx, `INSERT INTO public.tenant_invitations
		 (tenant_id, email, role, invited_by, code_hash, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		invitation.TenantID, invitation.Email, invitation.Role, invitation.InvitedBy,
		invitation.CodeHash, invitation.ExpiresAt)
	if err != nil {
		return fmt.Errorf("invitation_repo: create: %w", err)
	}
	return nil
}

func (r *InvitationRepo) ConsumeAndJoin(
	ctx context.Context,
	in domain.InvitationJoinInput,
) (*domain.InvitationJoinResult, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("invitation_repo: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var invitationID, tenantID, email, role, invitedBy string
	err = tx.QueryRow(ctx, `SELECT i.id, i.tenant_id, i.email, i.role, i.invited_by
		 FROM public.tenant_invitations i
		 JOIN public.tenants t ON t.id = i.tenant_id
		 WHERE i.code_hash = $1 AND i.consumed_at IS NULL AND i.expires_at > $2
		   AND t.status = 'active' AND t.deleted_at IS NULL
		 FOR UPDATE OF i`, in.CodeHash, in.Now).
		Scan(&invitationID, &tenantID, &email, &role, &invitedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvitationInvalid
		}
		return nil, fmt.Errorf("invitation_repo: lock invitation: %w", err)
	}
	if !strings.EqualFold(email, in.Identity.Email) {
		return nil, domain.ErrInvitationInvalid
	}

	var userID, globalRole string
	err = tx.QueryRow(ctx, `INSERT INTO public.users
		 (github_id, github_login, avatar_url, email, email_verified_at, last_login_at)
		 VALUES ($1, $2, $3, $4, now(), now())
		 ON CONFLICT (github_id) DO UPDATE SET
		   github_login = EXCLUDED.github_login,
		   avatar_url = EXCLUDED.avatar_url,
		   email = EXCLUDED.email,
		   email_verified_at = now(),
		   last_login_at = now(),
		   updated_at = now()
		 RETURNING id, global_role`,
		fmt.Sprintf("%d", in.Identity.GitHubID), in.Identity.GitHubLogin,
		in.Identity.AvatarURL, in.Identity.Email).
		Scan(&userID, &globalRole)
	if err != nil {
		return nil, fmt.Errorf("invitation_repo: upsert user: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.tenant_members
		 (tenant_id, user_id, role, invited_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tenantID, userID, role, invitedBy); err != nil {
		return nil, fmt.Errorf("invitation_repo: insert membership: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE public.tenant_invitations
		 SET consumed_at = $1, consumed_by = $2
		 WHERE id = $3 AND consumed_at IS NULL`, in.Now, userID, invitationID)
	if err != nil {
		return nil, fmt.Errorf("invitation_repo: consume invitation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, domain.ErrInvitationInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("invitation_repo: commit: %w", err)
	}
	return &domain.InvitationJoinResult{
		UserID: userID, TenantID: tenantID, Role: role, GlobalRole: globalRole,
	}, nil
}

func (r *InvitationRepo) ConsumeAndJoinExisting(
	ctx context.Context,
	in domain.ExistingInvitationJoinInput,
) (*domain.InvitationJoinResult, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("invitation_repo: begin existing join: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var invitationID, tenantID, role, invitedBy, globalRole string
	err = tx.QueryRow(ctx, `SELECT i.id, i.tenant_id, i.role, i.invited_by, u.global_role
		 FROM public.tenant_invitations i
		 JOIN public.tenants t ON t.id = i.tenant_id
		 JOIN public.users u ON u.id = $3
		   AND u.email_verified_at IS NOT NULL
		   AND lower(u.email) = lower(i.email)
		 WHERE i.code_hash = $1 AND i.consumed_at IS NULL AND i.expires_at > $2
		   AND t.status = 'active' AND t.deleted_at IS NULL
		 FOR UPDATE OF i`, in.CodeHash, in.Now, in.UserID).
		Scan(&invitationID, &tenantID, &role, &invitedBy, &globalRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInvitationInvalid
		}
		return nil, fmt.Errorf("invitation_repo: lock existing invitation: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.tenant_members
		 (tenant_id, user_id, role, invited_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tenantID, in.UserID, role, invitedBy); err != nil {
		return nil, fmt.Errorf("invitation_repo: insert existing membership: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE public.tenant_invitations
		 SET consumed_at = $1, consumed_by = $2
		 WHERE id = $3 AND consumed_at IS NULL`, in.Now, in.UserID, invitationID)
	if err != nil {
		return nil, fmt.Errorf("invitation_repo: consume existing invitation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, domain.ErrInvitationInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("invitation_repo: commit existing join: %w", err)
	}
	return &domain.InvitationJoinResult{
		UserID: in.UserID, TenantID: tenantID, Role: role, GlobalRole: globalRole,
	}, nil
}
