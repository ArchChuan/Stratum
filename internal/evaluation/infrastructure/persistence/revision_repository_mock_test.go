package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newRevision() domain.ResourceRevision {
	return domain.ResourceRevision{
		ID:               "rev-1",
		ResourceKind:     domain.ResourceKind("prompt"),
		ResourceID:       "r-1",
		ParentRevisionID: "parent-1",
		Source:           "upload",
		Status:           domain.RevisionStatusDraft,
		ContentHash:      "hash-1",
		PayloadRef:       "obj://r-1",
		PayloadHash:      "ph-1",
		SafeSummary:      map[string]any{"name": "v1"},
		CreatedBy:        "user-1",
		CreatedAt:        time.Now(),
	}
}

func TestPgRevisionRepository_Create_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO resource_revisions").
		WithArgs("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", `{"name":"v1"}`, "user-1", revision.CreatedAt, "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	stored, created, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "rev-1", stored.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Create_idempotentHit(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO resource_revisions").
		WithArgs("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", `{"name":"v1"}`, "user-1", revision.CreatedAt, "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectCommit()

	stored, created, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "rev-1", stored.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Create_idempotencyConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()
	revision.ContentHash = "different"

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO resource_revisions").
		WithArgs("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "different",
			"ph-1", "obj://r-1", `{"name":"v1"}`, "user-1", revision.CreatedAt, "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectRollback()

	_, _, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.ErrorIs(t, err, ErrRevisionIdempotencyConflict)
}

func TestPgRevisionRepository_Create_marshalSummaryFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()
	revision.SafeSummary = map[string]any{"bad": make(chan int)}

	_, _, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "marshal safe summary")
}

func TestPgRevisionRepository_Create_storedSummaryInvalid(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO resource_revisions").
		WithArgs("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", `{"name":"v1"}`, "user-1", revision.CreatedAt, "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("key-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", []byte(`{bad`), "user-1", revision.CreatedAt))
	mock.ExpectRollback()

	_, _, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.Error(t, err)
	require.ErrorContains(t, err, "decode safe summary")
}

func TestPgRevisionRepository_Get_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectCommit()

	stored, found, err := repo.Get(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1",
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "rev-1", stored.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Get_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("missing", "prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.Get(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "missing",
	})
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Publish_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectQuery("UPDATE resource_revisions SET status='published'").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "published", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectCommit()

	stored, err := repo.Publish(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1",
	})
	require.NoError(t, err)
	require.Equal(t, domain.RevisionStatusPublished, stored.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Publish_alreadyPublished(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectQuery("UPDATE resource_revisions SET status='published'").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "published", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectCommit()

	stored, err := repo.Publish(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1",
	})
	require.NoError(t, err)
	require.Equal(t, domain.RevisionStatusPublished, stored.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgRevisionRepository_Publish_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("UPDATE resource_revisions SET status='published'").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.Publish(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1",
	})
	require.ErrorIs(t, err, port.ErrCenterResourceNotFound)
}

func TestPgRevisionRepository_Publish_draftNotPublished(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectQuery("UPDATE resource_revisions SET status='published'").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT id, resource_kind, resource_id, COALESCE\\(parent_revision_id").
		WithArgs("rev-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "parent_revision_id", "source", "status",
			"content_hash", "payload_hash", "payload_ref", "safe_summary", "created_by", "created_at",
		}).AddRow("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", []byte(`{"name":"v1"}`), "user-1", revision.CreatedAt))
	mock.ExpectRollback()

	_, err := repo.Publish(context.Background(), "t1", domain.ResourceRef{
		Kind: "prompt", ResourceID: "r-1", RevisionID: "rev-1",
	})
	require.ErrorIs(t, err, domain.ErrRevisionNotPublished)
}

func TestPgRevisionRepository_Create_commitOutcomeUnknown(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgRevisionRepository{pool: mock}
	revision := newRevision()

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO resource_revisions").
		WithArgs("rev-1", "prompt", "r-1", "parent-1", "upload", "draft", "hash-1",
			"ph-1", "obj://r-1", `{"name":"v1"}`, "user-1", revision.CreatedAt, "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	_, _, err := repo.Create(context.Background(), "t1", revision, "key-1")
	require.Error(t, err)
	require.ErrorIs(t, err, port.ErrRevisionCommitUnknown)
}
