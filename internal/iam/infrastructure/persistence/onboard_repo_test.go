package persistence

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
)

func TestCreateGuestInDefaultTenantRetriesSerializationFailure(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := NewOnboardRepo(mock)

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)INSERT INTO users.*ON CONFLICT \\(github_id\\)").
		WithArgs("guest:retry", "guest", "", pgxmock.AnyArg()).
		WillReturnError(&pgconn.PgError{Code: "40001"})
	mock.ExpectRollback()

	expectSuccessfulGuestAttempt(mock, "guest:retry", "user-1", "tenant-1")

	userID, tenantID, err := repo.CreateGuestInDefaultTenant(context.Background(), "guest:retry", "guest", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateGuestInDefaultTenant() error = %v", err)
	}
	if userID != "user-1" || tenantID != "tenant-1" {
		t.Fatalf("CreateGuestInDefaultTenant() = (%q, %q), want (%q, %q)", userID, tenantID, "user-1", "tenant-1")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateGuestInDefaultTenantStopsOnPermanentFailure(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := NewOnboardRepo(mock)
	permanent := &pgconn.PgError{Code: "23505", Message: "constraint"}

	mock.ExpectBegin()
	mock.ExpectQuery("(?s)INSERT INTO users.*ON CONFLICT \\(github_id\\)").
		WithArgs("guest:permanent", "guest", "", pgxmock.AnyArg()).
		WillReturnError(permanent)
	mock.ExpectRollback()

	_, _, gotErr := repo.CreateGuestInDefaultTenant(context.Background(), "guest:permanent", "guest", "", time.Now().Add(time.Hour))
	if !errors.Is(gotErr, permanent) {
		t.Fatalf("error = %v, want wrapped permanent error", gotErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateGuestInDefaultTenantReplayUpsertsExistingUserAndMembership(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := NewOnboardRepo(mock)

	expectSuccessfulGuestAttempt(mock, "guest:replay", "existing-user", "default-tenant")

	userID, tenantID, err := repo.CreateGuestInDefaultTenant(context.Background(), "guest:replay", "guest", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateGuestInDefaultTenant() error = %v", err)
	}
	if userID != "existing-user" || tenantID != "default-tenant" {
		t.Fatalf("CreateGuestInDefaultTenant() = (%q, %q)", userID, tenantID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTenantSlugUsesTheFullGeneratedIDWhenNoOrganizationExists(t *testing.T) {
	first := "019fa3bd-105a-7ec6-aa08-eb8fb40ff228"
	second := "019fa3bd-ffff-7ec6-aa08-eb8fb40ff228"
	if tenantSlug(first, "") == tenantSlug(second, "") {
		t.Fatal("different UUIDv7 tenant IDs produced the same slug")
	}
	if got := tenantSlug(first, "acme"); got != "acme" {
		t.Fatalf("organization slug = %q, want acme", got)
	}
}

func expectSuccessfulGuestAttempt(mock pgxmock.PgxPoolIface, githubID, userID, tenantID string) {
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)INSERT INTO users.*ON CONFLICT \\(github_id\\)").
		WithArgs(githubID, "guest", "", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userID))
	mock.ExpectQuery("SELECT id FROM tenants WHERE is_default = true").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(tenantID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO tenant_members (tenant_id, user_id, role)")).
		WithArgs(tenantID, userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
}
