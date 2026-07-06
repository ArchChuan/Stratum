package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type DocRepo struct {
	db *pgxpool.Pool
}

func NewDocRepo(db *pgxpool.Pool) *DocRepo {
	return &DocRepo{db: db}
}

func (r *DocRepo) ExistsByHash(ctx context.Context, tenantID, workspaceID, hash string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM "%s".knowledge_docs WHERE workspace_id=$1 AND content_hash=$2)`, schemaFor(tenantID)),
		workspaceID, hash,
	).Scan(&exists)
	return exists, err
}

func (r *DocRepo) Save(ctx context.Context, tenantID, kbID string, doc *domain.Document) error {
	status := doc.IngestStatus
	if status == "" {
		status = "processing"
	}
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`INSERT INTO "%s".knowledge_docs
			(id, workspace_id, title, content_hash, ingest_status, total_chunks, ingest_started_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (id) DO NOTHING`, schemaFor(tenantID)),
		doc.ID, kbID, doc.Source, doc.ContentHash, status, doc.TotalChunks,
	)
	return err
}

func (r *DocRepo) List(ctx context.Context, tenantID, kbID string) ([]*domain.Document, error) {
	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, workspace_id, title, COALESCE(content_hash, ''),
			ingest_status, ingest_error, processed_chunks, total_chunks,
			created_at, ingest_started_at, ingest_finished_at
			FROM "%s".knowledge_docs
			WHERE workspace_id=$1
			ORDER BY created_at DESC`, schemaFor(tenantID)),
		kbID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []*domain.Document
	for rows.Next() {
		d := &domain.Document{}
		if err := rows.Scan(
			&d.ID, &d.KBID, &d.Source, &d.ContentHash,
			&d.IngestStatus, &d.IngestError, &d.ProcessedChunks, &d.TotalChunks,
			&d.CreatedAt, &d.IngestStartedAt, &d.IngestFinishedAt,
		); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (r *DocRepo) CountByWorkspace(ctx context.Context, tenantID, workspaceID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM "%s".knowledge_docs WHERE workspace_id=$1`, schemaFor(tenantID)),
		workspaceID,
	).Scan(&count)
	return count, err
}

func (r *DocRepo) Delete(ctx context.Context, tenantID, kbID, docID string) error {
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM "%s".knowledge_docs WHERE workspace_id=$1 AND id=$2`, schemaFor(tenantID)),
		kbID, docID,
	)
	return err
}

func (r *DocRepo) MarkIngestStarted(ctx context.Context, tenantID, docID string, totalChunks int) error {
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`UPDATE "%s".knowledge_docs
			SET ingest_status='processing', total_chunks=$2, processed_chunks=0,
			    ingest_started_at=NOW(), ingest_error=''
			WHERE id=$1`, schemaFor(tenantID)),
		docID, totalChunks,
	)
	return err
}

func (r *DocRepo) MarkIngestCompleted(ctx context.Context, tenantID, docID string, processedChunks int) error {
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`UPDATE "%s".knowledge_docs
			SET ingest_status='completed', processed_chunks=$2,
			    ingest_finished_at=NOW(), ingest_error=''
			WHERE id=$1`, schemaFor(tenantID)),
		docID, processedChunks,
	)
	return err
}

func (r *DocRepo) MarkIngestFailed(ctx context.Context, tenantID, docID, errMsg string) error {
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`UPDATE "%s".knowledge_docs
			SET ingest_status='failed', ingest_finished_at=NOW(), ingest_error=$2
			WHERE id=$1`, schemaFor(tenantID)),
		docID, errMsg,
	)
	return err
}

func (r *DocRepo) RecoverStuckIngests(ctx context.Context, tenantID string, threshold time.Duration) (int, error) {
	tag, err := r.db.Exec(ctx,
		fmt.Sprintf(`UPDATE "%s".knowledge_docs
			SET ingest_status='failed',
			    ingest_finished_at=NOW(),
			    ingest_error='ingest aborted by server restart'
			WHERE ingest_status='processing'
			  AND ingest_started_at < NOW() - $1::interval`, schemaFor(tenantID)),
		fmt.Sprintf("%d seconds", int(threshold.Seconds())),
	)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
