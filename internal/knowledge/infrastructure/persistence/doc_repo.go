package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM knowledge_docs WHERE workspace_id=$1 AND content_hash=$2)`,
			workspaceID, hash,
		).Scan(&exists)
	})
	return exists, err
}

func (r *DocRepo) Save(ctx context.Context, tenantID, kbID string, doc *domain.Document) error {
	status := doc.IngestStatus
	if status == "" {
		status = "processing"
	}
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO knowledge_docs
			(id, workspace_id, title, content_hash, ingest_status, total_chunks, ingest_started_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (id) DO NOTHING`, doc.ID, kbID, doc.Source, doc.ContentHash, status, doc.TotalChunks)
		return err
	})
}

func (r *DocRepo) List(ctx context.Context, tenantID, kbID string) ([]*domain.Document, error) {
	var docs []*domain.Document
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, workspace_id, title, COALESCE(content_hash, ''),
			ingest_status, ingest_error, processed_chunks, total_chunks,
			created_at, ingest_started_at, ingest_finished_at
			FROM knowledge_docs
			WHERE workspace_id=$1
			ORDER BY created_at DESC`, kbID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d := &domain.Document{}
			if err := rows.Scan(
				&d.ID, &d.KBID, &d.Source, &d.ContentHash,
				&d.IngestStatus, &d.IngestError, &d.ProcessedChunks, &d.TotalChunks,
				&d.CreatedAt, &d.IngestStartedAt, &d.IngestFinishedAt,
			); err != nil {
				return err
			}
			docs = append(docs, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

func (r *DocRepo) CountByWorkspace(ctx context.Context, tenantID, workspaceID string) (int, error) {
	var count int
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM knowledge_docs WHERE workspace_id=$1`, workspaceID).Scan(&count)
	})
	return count, err
}

func (r *DocRepo) Delete(ctx context.Context, tenantID, kbID, docID string) error {
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, table := range []string{"knowledge_chunks", "knowledge_parent_chunks"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE workspace_id=$1 AND doc_id=$2`, kbID, docID); err != nil {
				return fmt.Errorf("delete %s: %w", table, err)
			}
		}
		tag, err := tx.Exec(ctx, `DELETE FROM knowledge_docs WHERE workspace_id=$1 AND id=$2`, kbID, docID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrDocumentNotFound
		}
		return nil
	})
}

func (r *DocRepo) MarkIngestStarted(ctx context.Context, tenantID, docID string, totalChunks int) error {
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE knowledge_docs
			SET ingest_status='processing', total_chunks=$2, processed_chunks=0,
			    ingest_started_at=NOW(), ingest_error=''
			WHERE id=$1`, docID, totalChunks)
		return err
	})
}

func (r *DocRepo) MarkIngestCompleted(ctx context.Context, tenantID, docID string, processedChunks int) error {
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE knowledge_docs
			SET ingest_status='completed', processed_chunks=$2,
			    ingest_finished_at=NOW(), ingest_error=''
			WHERE id=$1`, docID, processedChunks)
		return err
	})
}

func (r *DocRepo) MarkIngestFailed(ctx context.Context, tenantID, docID, errMsg string) error {
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE knowledge_docs
			SET ingest_status='failed', ingest_finished_at=NOW(), ingest_error=$2
			WHERE id=$1`, docID, errMsg)
		return err
	})
}

func (r *DocRepo) RecoverStuckIngests(ctx context.Context, tenantID string, threshold time.Duration) (int, error) {
	var count int
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE knowledge_docs
			SET ingest_status='failed',
			    ingest_finished_at=NOW(),
			    ingest_error='ingest aborted by server restart'
			WHERE ingest_status='processing'
			  AND ingest_started_at < NOW() - $1::interval`, fmt.Sprintf("%d seconds", int(threshold.Seconds())))
		count = int(tag.RowsAffected())
		return err
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
