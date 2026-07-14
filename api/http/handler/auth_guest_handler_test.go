package handler

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

func TestNewGuestLoginResponseIncludesUserIdentity(t *testing.T) {
	guest := &application.GuestAccount{
		UserID:      "guest-user",
		TenantID:    "default-tenant",
		GitHubLogin: "guest-demo",
		AvatarURL:   "",
	}

	resp := newGuestLoginResponse(guest, "access-token", domain.SystemRoleUser)

	if resp.AccessToken != "access-token" || resp.TenantID != "default-tenant" {
		t.Fatalf("unexpected token response: %+v", resp)
	}
	if resp.User.Sub != "guest-user" || resp.User.TenantID != "default-tenant" {
		t.Fatalf("unexpected user identity: %+v", resp.User)
	}
	if resp.User.Role != "member" || resp.User.SystemRole != string(domain.SystemRoleUser) {
		t.Fatalf("unexpected user roles: %+v", resp.User)
	}
	if resp.User.GitHubLogin != "guest-demo" {
		t.Fatalf("unexpected guest login: %+v", resp.User)
	}
}
