package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type invitationRepoFake struct {
	created domain.TenantInvitation
	joined  domain.InvitationJoinInput
	result  domain.InvitationJoinResult
	err     error
}

func (f *invitationRepoFake) ConsumeAndJoinExisting(_ context.Context, in domain.ExistingInvitationJoinInput) (*domain.InvitationJoinResult, error) {
	f.joined = domain.InvitationJoinInput{CodeHash: in.CodeHash, Now: in.Now}
	return &f.result, f.err
}

func (f *invitationRepoFake) Create(_ context.Context, invitation domain.TenantInvitation) error {
	f.created = invitation
	return f.err
}

func (f *invitationRepoFake) ConsumeAndJoin(_ context.Context, in domain.InvitationJoinInput) (*domain.InvitationJoinResult, error) {
	f.joined = in
	return &f.result, f.err
}

func TestInvitationServiceCreateStoresHashOnly(t *testing.T) {
	repo := &invitationRepoFake{}
	svc := NewInvitationService(repo)

	code, err := svc.Create(context.Background(), "tenant-1", "owner-1", "owner", " New.User@Example.COM ", "member")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if code == "" {
		t.Fatal("expected one-time invitation code")
	}
	if repo.created.CodeHash == "" || repo.created.CodeHash == code {
		t.Fatal("repository must receive only a non-plaintext code hash")
	}
	if repo.created.Email != "new.user@example.com" {
		t.Fatalf("normalized email=%q", repo.created.Email)
	}
	if repo.created.ExpiresAt.Before(time.Now()) {
		t.Fatal("invitation expiry must be in the future")
	}
}

func TestInvitationServiceCreateRejectsMember(t *testing.T) {
	svc := NewInvitationService(&invitationRepoFake{})
	if _, err := svc.Create(context.Background(), "tenant-1", "member-1", "member", "new.user@example.com", "member"); err != ErrForbiddenAdminOrOwner {
		t.Fatalf("Create error=%v, want ErrForbiddenAdminOrOwner", err)
	}
}

func TestInvitationServiceJoinBindsVerifiedEmail(t *testing.T) {
	repo := &invitationRepoFake{result: domain.InvitationJoinResult{UserID: "user-1", TenantID: "tenant-1", Role: "member"}}
	svc := NewInvitationService(repo)

	result, err := svc.Join(context.Background(), "one-time-code", domain.InvitationIdentity{
		GitHubID: 42, GitHubLogin: "new-user", Email: "New.User@Example.COM",
	})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if result.TenantID != "tenant-1" {
		t.Fatalf("tenant=%q", result.TenantID)
	}
	if repo.joined.CodeHash == "" || repo.joined.CodeHash == "one-time-code" {
		t.Fatal("join repository must receive only a code hash")
	}
	if repo.joined.Identity.Email != "new.user@example.com" {
		t.Fatalf("join email=%q", repo.joined.Identity.Email)
	}
}

func TestInvitationServiceJoinExistingHashesCode(t *testing.T) {
	repo := &invitationRepoFake{result: domain.InvitationJoinResult{UserID: "user-1", TenantID: "tenant-1", Role: "member"}}
	svc := NewInvitationService(repo)
	result, err := svc.JoinExisting(context.Background(), "one-time-code", "user-1")
	if err != nil {
		t.Fatalf("JoinExisting: %v", err)
	}
	if result.TenantID != "tenant-1" || repo.joined.CodeHash == "one-time-code" || repo.joined.CodeHash == "" {
		t.Fatalf("result=%#v hash=%q", result, repo.joined.CodeHash)
	}
}
