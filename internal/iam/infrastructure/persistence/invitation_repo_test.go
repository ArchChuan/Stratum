package persistence

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func TestInvitationRepoConsumeAndJoinCommitsAtomically(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()

	pool.ExpectBegin()
	pool.ExpectQuery(regexp.QuoteMeta(`SELECT i.id, i.tenant_id, i.email, i.role, i.invited_by
		 FROM public.tenant_invitations i
		 JOIN public.tenants t ON t.id = i.tenant_id
		 WHERE i.code_hash = $1 AND i.consumed_at IS NULL AND i.expires_at > $2
		   AND t.status = 'active' AND t.deleted_at IS NULL
		 FOR UPDATE OF i`)).WithArgs("hash", now).
		WillReturnRows(pgxmock.NewRows([]string{"id", "tenant_id", "email", "role", "invited_by"}).
			AddRow("invite-1", "tenant-1", "new.user@example.com", "member", "owner-1"))
	pool.ExpectQuery("INSERT INTO public.users").
		WithArgs("42", "new-user", "https://example.com/avatar", "new.user@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "global_role"}).AddRow("user-1", "user"))
	pool.ExpectExec("INSERT INTO public.tenant_members").
		WithArgs("tenant-1", "user-1", "member", "owner-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("UPDATE public.tenant_invitations").
		WithArgs(now, "user-1", "invite-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()

	result, err := repo.ConsumeAndJoin(context.Background(), domain.InvitationJoinInput{
		CodeHash: "hash", Now: now,
		Identity: domain.InvitationIdentity{
			GitHubID: 42, GitHubLogin: "new-user", AvatarURL: "https://example.com/avatar", Email: "new.user@example.com",
		},
	})
	if err != nil {
		t.Fatalf("ConsumeAndJoin: %v", err)
	}
	if result.UserID != "user-1" || result.TenantID != "tenant-1" || result.Role != "member" {
		t.Fatalf("result=%#v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInvitationRepoConsumeAndJoinRejectsMissingCode(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()

	pool.ExpectBegin()
	pool.ExpectQuery("SELECT i.id, i.tenant_id").WithArgs("missing", now).WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()
	_, err = repo.ConsumeAndJoin(context.Background(), domain.InvitationJoinInput{
		CodeHash: "missing", Now: now,
		Identity: domain.InvitationIdentity{GitHubID: 42, Email: "new.user@example.com"},
	})
	if err != domain.ErrInvitationInvalid {
		t.Fatalf("error=%v, want ErrInvitationInvalid", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInvitationRepoConsumeAndJoinExistingRequiresVerifiedMatchingEmail(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewInvitationRepo(pool)
	now := time.Now().UTC()
	pool.ExpectBegin()
	pool.ExpectQuery("JOIN public.users u").WithArgs("hash", now, "user-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "tenant_id", "role", "invited_by", "global_role"}).
			AddRow("invite-1", "tenant-1", "member", "owner-1", "user"))
	pool.ExpectExec("INSERT INTO public.tenant_members").
		WithArgs("tenant-1", "user-1", "member", "owner-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("UPDATE public.tenant_invitations").
		WithArgs(now, "user-1", "invite-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectCommit()
	result, err := repo.ConsumeAndJoinExisting(context.Background(), domain.ExistingInvitationJoinInput{
		CodeHash: "hash", UserID: "user-1", Now: now,
	})
	if err != nil {
		t.Fatalf("ConsumeAndJoinExisting: %v", err)
	}
	if result.TenantID != "tenant-1" || result.UserID != "user-1" {
		t.Fatalf("result=%#v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
