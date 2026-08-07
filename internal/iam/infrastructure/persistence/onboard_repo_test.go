package persistence

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v2"
)

func TestCreateGuestSandboxTenantRetriesSerializationFailure(t *testing.T) {
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

	expectSuccessfulGuestAttempt(mock, "guest:retry", "user-1")

	userID, tenantID, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:retry", "guest", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateGuestSandboxTenant() error = %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("CreateGuestSandboxTenant() user = %q, want %q", userID, "user-1")
	}
	assertSandboxTenantID(t, tenantID)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateGuestSandboxTenantStopsOnPermanentFailure(t *testing.T) {
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

	_, _, gotErr := repo.CreateGuestSandboxTenant(context.Background(), "guest:permanent", "guest", "", time.Now().Add(time.Hour))
	if !errors.Is(gotErr, permanent) {
		t.Fatalf("error = %v, want wrapped permanent error", gotErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateGuestSandboxTenantReplayUpsertsExistingUserAndMembership(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := NewOnboardRepo(mock)

	expectSuccessfulGuestAttempt(mock, "guest:replay", "existing-user")

	userID, tenantID, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:replay", "guest", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("CreateGuestSandboxTenant() error = %v", err)
	}
	if userID != "existing-user" {
		t.Fatalf("CreateGuestSandboxTenant() user = %q, want existing-user", userID)
	}
	assertSandboxTenantID(t, tenantID)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateGuestSandboxTenantNeverQueriesDefaultTenant(t *testing.T) {
	// Regression guard: the sandbox flow must not touch the default tenant at
	// all (no SELECT on tenants WHERE is_default = true, no default-tenant
	// membership insert). If the implementation regresses, the strict pgxmock
	// expectation set below fails.
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	repo := NewOnboardRepo(mock)

	expectSuccessfulGuestAttempt(mock, "guest:isolated", "user-1")

	if _, _, err := repo.CreateGuestSandboxTenant(context.Background(), "guest:isolated", "guest", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateGuestSandboxTenant() error = %v", err)
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

// expectSuccessfulGuestAttempt records the strict SQL expectation set for the
// sandbox provisioning flow. The sandbox tenant ID is generated inside the
// repo, so it is matched with AnyArg; the returned tenant ID is asserted
// non-empty (valid UUID) by the callers.
func expectSuccessfulGuestAttempt(mock pgxmock.PgxPoolIface, githubID, userID string) {
	mock.ExpectBegin()
	mock.ExpectQuery("(?s)INSERT INTO users.*ON CONFLICT \\(github_id\\)").
		WithArgs(githubID, "guest", "", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO tenants (id, name, slug, status)")).
		WithArgs(pgxmock.AnyArg(), "guest", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO tenant_members (tenant_id, user_id, role)")).
		WithArgs(pgxmock.AnyArg(), userID).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
}

func assertSandboxTenantID(t *testing.T, tenantID string) {
	t.Helper()
	if _, err := uuid.Parse(tenantID); err != nil {
		t.Fatalf("sandbox tenant id = %q, want valid UUID", tenantID)
	}
}
