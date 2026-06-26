package persistence

import (
	"context"
	"fmt"

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
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`INSERT INTO "%s".knowledge_docs (id, workspace_id, title, content_hash)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (id) DO NOTHING`, schemaFor(tenantID)),
		doc.ID, kbID, doc.Source, doc.ContentHash,
	)
	return err
}

func (r *DocRepo) List(ctx context.Context, tenantID, kbID string) ([]*domain.Document, error) {
	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, workspace_id, title, COALESCE(content_hash, '') FROM "%s".knowledge_docs WHERE workspace_id=$1`, schemaFor(tenantID)),
		kbID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []*domain.Document
	for rows.Next() {
		d := &domain.Document{}
		if err := rows.Scan(&d.ID, &d.KBID, &d.Source, &d.ContentHash); err != nil {
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
