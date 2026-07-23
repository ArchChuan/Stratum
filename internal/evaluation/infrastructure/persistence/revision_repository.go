package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRevisionIdempotencyConflict = errors.New("revision idempotency key conflict")
var ErrRevisionCommitUnknown = port.ErrRevisionCommitUnknown

type RevisionObjectStoreAdapter struct{ Store objectstore.Store }

func (a RevisionObjectStoreAdapter) Put(ctx context.Context, p port.RevisionPayload) (port.RevisionPayloadRef, error) {
	r, err := a.Store.Put(ctx, objectstore.Payload{TenantID: p.TenantID, Namespace: p.Namespace, ID: p.ID, Value: p.Value})
	return port.RevisionPayloadRef{URI: r.URI, SHA256: r.SHA256, SizeBytes: r.SizeBytes}, err
}
func (a RevisionObjectStoreAdapter) Get(ctx context.Context, r port.RevisionPayloadRef) ([]byte, error) {
	return a.Store.Get(ctx, objectstore.Reference{URI: r.URI, SHA256: r.SHA256, SizeBytes: r.SizeBytes})
}
func (a RevisionObjectStoreAdapter) Delete(ctx context.Context, r port.RevisionPayloadRef) error {
	return a.Store.Delete(ctx, objectstore.Reference{URI: r.URI, SHA256: r.SHA256, SizeBytes: r.SizeBytes})
}

type PgRevisionRepository struct {
	pool *pgxpool.Pool
}

func NewPgRevisionRepository(pool *pgxpool.Pool) *PgRevisionRepository {
	return &PgRevisionRepository{pool: pool}
}

func (r *PgRevisionRepository) Create(
	ctx context.Context,
	tenantID string,
	revision domain.ResourceRevision,
	idempotencyKey string,
) (domain.ResourceRevision, bool, error) {
	summaryJSON, err := json.Marshal(revision.SafeSummary)
	if err != nil {
		return domain.ResourceRevision{}, false, fmt.Errorf("revision repository: marshal safe summary: %w", err)
	}

	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	stored := domain.ResourceRevision{}
	created := false
	err = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx,
			`INSERT INTO resource_revisions
			 (id, resource_kind, resource_id, parent_revision_id, source, status, content_hash,
			  payload_hash, payload_ref, safe_summary, created_by, created_at, idempotency_key)
			 VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13)
			 ON CONFLICT (idempotency_key) WHERE idempotency_key <> '' DO NOTHING`,
			revision.ID, string(revision.ResourceKind), revision.ResourceID, revision.ParentRevisionID,
			string(revision.Source), string(revision.Status), revision.ContentHash, revision.PayloadHash,
			revision.PayloadRef, string(summaryJSON), revision.CreatedBy, revision.CreatedAt, idempotencyKey,
		)
		if err != nil {
			return fmt.Errorf("revision repository: insert revision: %w", err)
		}
		if result.RowsAffected() == 1 {
			stored = revision
			created = true
			return nil
		}

		stored, err = scanRevision(tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, COALESCE(parent_revision_id, ''), source, status,
			        content_hash, payload_hash, payload_ref, safe_summary, created_by, created_at
			 FROM resource_revisions WHERE idempotency_key=$1`, idempotencyKey))
		if err != nil {
			return fmt.Errorf("revision repository: load idempotent revision: %w", err)
		}
		storedSummaryJSON, err := json.Marshal(stored.SafeSummary)
		if err != nil {
			return fmt.Errorf("revision repository: marshal stored safe summary: %w", err)
		}
		if stored.ResourceKind != revision.ResourceKind || stored.ResourceID != revision.ResourceID ||
			stored.ParentRevisionID != revision.ParentRevisionID || stored.Source != revision.Source ||
			stored.ContentHash != revision.ContentHash || stored.CreatedBy != revision.CreatedBy ||
			!bytes.Equal(storedSummaryJSON, summaryJSON) {
			return fmt.Errorf("%w: %s", ErrRevisionIdempotencyConflict, idempotencyKey)
		}
		return nil
	})
	if err != nil {
		return domain.ResourceRevision{}, false, mapRevisionRepositoryError(err)
	}
	return stored, created, nil
}

func mapRevisionRepositoryError(err error) error {
	if errors.Is(err, postgres.ErrCommitOutcomeUnknown) {
		return errors.Join(ErrRevisionCommitUnknown, err)
	}
	return err
}

func (r *PgRevisionRepository) Get(
	ctx context.Context,
	tenantID string,
	ref domain.ResourceRef,
) (domain.ResourceRevision, bool, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var revision domain.ResourceRevision
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		revision, err = scanRevision(tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, COALESCE(parent_revision_id, ''), source, status,
			        content_hash, payload_hash, payload_ref, safe_summary, created_by, created_at
			 FROM resource_revisions WHERE id=$1 AND resource_kind=$2 AND resource_id=$3`,
			ref.RevisionID, string(ref.Kind), ref.ResourceID))
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("revision repository: get revision: %w", err)
		}
		found = true
		return nil
	})
	return revision, found, err
}

func (r *PgRevisionRepository) Publish(
	ctx context.Context, tenantID string, ref domain.ResourceRef,
) (domain.ResourceRevision, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var published domain.ResourceRevision
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		published, err = scanRevision(tx.QueryRow(ctx,
			`UPDATE resource_revisions SET status='published'
			 WHERE id=$1 AND resource_kind=$2 AND resource_id=$3 AND status='draft'
			 RETURNING id, resource_kind, resource_id, COALESCE(parent_revision_id, ''), source, status,
			           content_hash, payload_hash, payload_ref, safe_summary, created_by, created_at`,
			ref.RevisionID, string(ref.Kind), ref.ResourceID))
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("revision repository: publish revision: %w", err)
		}
		existing, loadErr := scanRevision(tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, COALESCE(parent_revision_id, ''), source, status,
			        content_hash, payload_hash, payload_ref, safe_summary, created_by, created_at
			 FROM resource_revisions WHERE id=$1 AND resource_kind=$2 AND resource_id=$3`,
			ref.RevisionID, string(ref.Kind), ref.ResourceID))
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return port.ErrCenterResourceNotFound
		}
		if loadErr != nil {
			return fmt.Errorf("revision repository: load publish state: %w", loadErr)
		}
		if existing.Status != domain.RevisionStatusPublished {
			return domain.ErrRevisionNotPublished
		}
		published = existing
		return nil
	})
	if err != nil {
		return domain.ResourceRevision{}, mapRevisionRepositoryError(err)
	}
	return published, nil
}

type revisionRow interface {
	Scan(...any) error
}

func scanRevision(row revisionRow) (domain.ResourceRevision, error) {
	var revision domain.ResourceRevision
	var kind, source, status string
	var summaryJSON []byte
	if err := row.Scan(
		&revision.ID, &kind, &revision.ResourceID, &revision.ParentRevisionID, &source, &status,
		&revision.ContentHash, &revision.PayloadHash, &revision.PayloadRef, &summaryJSON,
		&revision.CreatedBy, &revision.CreatedAt,
	); err != nil {
		return domain.ResourceRevision{}, err
	}
	revision.ResourceKind = domain.ResourceKind(kind)
	revision.Source = domain.RevisionSource(source)
	revision.Status = domain.RevisionStatus(status)
	if err := json.Unmarshal(summaryJSON, &revision.SafeSummary); err != nil {
		return domain.ResourceRevision{}, fmt.Errorf("decode safe summary: %w", err)
	}
	return revision, nil
}
