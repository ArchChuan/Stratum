//go:build integration

package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgRevisionRepositoryCreateDuplicateGetAndTenantIsolation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set; PostgreSQL revision repository integration test requires a real tenant database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	const tenantID = "revision_repo_test"
	const otherTenantID = "revision_repo_other_test"
	for _, id := range []string{tenantID, otherTenantID} {
		if err := postgres.ProvisionTenantSchema(ctx, pool, id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range []string{tenantID, otherTenantID} {
			_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, id))
		}
	})

	repo := NewPgRevisionRepository(pool)
	revision := integrationRevision("revision-1")
	createdRevision, created, err := repo.Create(ctx, tenantID, revision, "request-1")
	if err != nil || !created || createdRevision.ID != revision.ID {
		t.Fatalf("unexpected create result: revision=%+v created=%v err=%v", createdRevision, created, err)
	}

	duplicate := integrationRevision("revision-duplicate")
	existing, created, err := repo.Create(ctx, tenantID, duplicate, "request-1")
	if err != nil || created || existing.ID != revision.ID {
		t.Fatalf("unexpected duplicate result: revision=%+v created=%v err=%v", existing, created, err)
	}
	conflict := integrationRevision("revision-conflict")
	conflict.ResourceID = "other-resource"
	if _, _, err := repo.Create(ctx, tenantID, conflict, "request-1"); !errors.Is(err, ErrRevisionIdempotencyConflict) {
		t.Fatalf("expected idempotency fingerprint conflict, got %v", err)
	}
	summaryConflict := integrationRevision("revision-summary-conflict")
	summaryConflict.SafeSummary = map[string]any{"resource_name": "different"}
	if _, _, err := repo.Create(ctx, tenantID, summaryConflict, "request-1"); !errors.Is(err, ErrRevisionIdempotencyConflict) {
		t.Fatalf("expected summary fingerprint conflict, got %v", err)
	}

	ref := domain.ResourceRef{Kind: revision.ResourceKind, ResourceID: revision.ResourceID, RevisionID: revision.ID}
	got, found, err := repo.Get(ctx, tenantID, ref)
	if err != nil || !found || got.PayloadRef != revision.PayloadRef || got.SafeSummary["resource_name"] != "baseline" {
		t.Fatalf("unexpected get result: revision=%+v found=%v err=%v", got, found, err)
	}
	if _, found, err := repo.Get(ctx, otherTenantID, ref); err != nil || found {
		t.Fatalf("cross-tenant lookup leaked revision: found=%v err=%v", found, err)
	}
	published, err := repo.Publish(ctx, tenantID, ref)
	if err != nil || published.Status != domain.RevisionStatusPublished {
		t.Fatalf("unexpected publish result: revision=%+v err=%v", published, err)
	}
	replayed, err := repo.Publish(ctx, tenantID, ref)
	if err != nil || replayed.Status != domain.RevisionStatusPublished {
		t.Fatalf("publish replay must be idempotent: revision=%+v err=%v", replayed, err)
	}
	if _, err := repo.Publish(ctx, otherTenantID, ref); !errors.Is(err, port.ErrCenterResourceNotFound) {
		t.Fatalf("cross-tenant publish must be not found, got %v", err)
	}

	invalid := integrationRevision("revision-invalid-parent")
	invalid.ParentRevisionID = "missing-parent"
	if _, _, err := repo.Create(ctx, tenantID, invalid, "request-invalid"); err == nil {
		t.Fatal("expected foreign-key failure")
	}
	invalidRef := domain.ResourceRef{Kind: invalid.ResourceKind, ResourceID: invalid.ResourceID, RevisionID: invalid.ID}
	if _, found, err := repo.Get(ctx, tenantID, invalidRef); err != nil || found {
		t.Fatalf("failed transaction left revision metadata behind: found=%v err=%v", found, err)
	}
}

func integrationRevision(id string) domain.ResourceRevision {
	return domain.ResourceRevision{
		ID:           id,
		ResourceKind: domain.ResourceKindSkill,
		ResourceID:   "skill-1",
		Source:       domain.RevisionSourceManual,
		Status:       domain.RevisionStatusDraft,
		ContentHash:  "content-hash",
		PayloadRef:   "object://revisions/payload.enc",
		PayloadHash:  "payload-hash",
		SafeSummary:  map[string]any{"resource_name": "baseline"},
		CreatedBy:    "user-1",
	}
}
