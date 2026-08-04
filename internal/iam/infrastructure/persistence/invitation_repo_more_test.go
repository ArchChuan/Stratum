package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestInvitationRepo_Create(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()

	pool.ExpectExec(`INSERT INTO public.tenant_invitations`).
		WithArgs("t1", "a@b.com", "member", "u1", "hash", now).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(context.Background(), domain.TenantInvitation{
		TenantID: "t1", Email: "a@b.com", Role: "member", InvitedBy: "u1",
		CodeHash: "hash", ExpiresAt: now,
	})
	require.NoError(t, err)
	require.NoError(t, pool.ExpectationsWereMet())
}

func TestInvitationRepo_Create_ExecFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	repo := NewInvitationRepo(pool)

	pool.ExpectExec(`INSERT INTO public.tenant_invitations`).
		WithArgs("t1", "a@b.com", "member", "u1", "hash", pgxmock.AnyArg()).
		WillReturnError(errAny)

	err = repo.Create(context.Background(), domain.TenantInvitation{
		TenantID: "t1", Email: "a@b.com", Role: "member", InvitedBy: "u1",
		CodeHash: "hash", ExpiresAt: time.Now().UTC(),
	})
	require.ErrorContains(t, err, "invitation_repo: create")
	require.ErrorIs(t, err, errAny)
}

func TestInvitationRepo_ConsumeAndJoin_EmailMismatch(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()

	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT i.id, i.tenant_id, i.email, i.role, i.invited_by`).
		WithArgs("hash", now).
		WillReturnRows(pgxmock.NewRows([]string{"id", "tenant_id", "email", "role", "invited_by"}).
			AddRow("invite-1", "tenant-1", "other@example.com", "member", "owner-1"))
	pool.ExpectRollback()

	_, err = repo.ConsumeAndJoin(context.Background(), domain.InvitationJoinInput{
		CodeHash: "hash", Now: now,
		Identity: domain.InvitationIdentity{GitHubID: 42, Email: "new.user@example.com"},
	})
	require.ErrorIs(t, err, domain.ErrInvitationInvalid)
}

func TestInvitationRepo_ConsumeAndJoin_ConsumeNoRows(t *testing.T) {
	pool, err := pgxmock.NewPool()
	require.NoError(t, err)
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()

	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT i.id, i.tenant_id, i.email, i.role, i.invited_by`).
		WithArgs("hash", now).
		WillReturnRows(pgxmock.NewRows([]string{"id", "tenant_id", "email", "role", "invited_by"}).
			AddRow("invite-1", "tenant-1", "new.user@example.com", "member", "owner-1"))
	pool.ExpectQuery(`INSERT INTO public.users`).
		WithArgs("42", "new-user", "", "new.user@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "global_role"}).AddRow("user-1", "user"))
	pool.ExpectExec(`INSERT INTO public.tenant_members`).
		WithArgs("tenant-1", "user-1", "member", "owner-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec(`UPDATE public.tenant_invitations`).
		WithArgs(now, "user-1", "invite-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0)) // 已被并发消费
	pool.ExpectRollback()

	_, err = repo.ConsumeAndJoin(context.Background(), domain.InvitationJoinInput{
		CodeHash: "hash", Now: now,
		Identity: domain.InvitationIdentity{GitHubID: 42, GitHubLogin: "new-user", Email: "new.user@example.com"},
	})
	require.ErrorIs(t, err, domain.ErrInvitationInvalid)
}
