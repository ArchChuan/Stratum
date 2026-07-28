package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	"github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

type onboardEmailRepoFake struct {
	port.OnboardRepo
	input domain.AutoJoinInput
}

func (f *onboardEmailRepoFake) AutoJoinDefaultTenant(_ context.Context, in domain.AutoJoinInput) (string, string, string, error) {
	f.input = in
	return "user-1", "tenant-1", "user", nil
}

func TestAutoJoinDefaultTenantCarriesVerifiedEmail(t *testing.T) {
	repo := &onboardEmailRepoFake{}
	svc := NewOnboardService(repo)
	_, _, _, err := svc.AutoJoinDefaultTenant(
		context.Background(), 42, "new-user", "avatar", "new.user@example.com", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if repo.input.Email != "new.user@example.com" {
		t.Fatalf("email=%q", repo.input.Email)
	}
}
