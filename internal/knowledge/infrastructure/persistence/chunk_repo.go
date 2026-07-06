package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type ChunkRepo struct {
	db *pgxpool.Pool
}

func NewChunkRepo(db *pgxpool.Pool) *ChunkRepo {
	return &ChunkRepo{db: db}
}

func (r *ChunkRepo) InsertBatch(ctx context.Context, tenantID, workspaceID string, chunks []domain.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	schema := schemaFor(tenantID)
	batch := &pgx.Batch{}
	for _, c := range chunks {
		batch.Queue(
			fmt.Sprintf(`INSERT INTO "%s".knowledge_chunks(id, workspace_id, doc_id, chunk_index, content)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, schema),
			c.ID, workspaceID, c.DocID, c.Index, c.Text,
		)
	}
	br := r.db.SendBatch(ctx, batch)
	defer br.Close() //nolint:errcheck
	for range chunks {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("chunk_repo: insert: %w", err)
		}
	}
	return nil
}

func (r *ChunkRepo) KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error) {
	schema := schemaFor(tenantID)
	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, doc_id, chunk_index, content
			FROM "%s".knowledge_chunks
			WHERE workspace_id = $1
			  AND tsv @@ plainto_tsquery('simple', $2)
			ORDER BY ts_rank(tsv, plainto_tsquery('simple', $2)) DESC
			LIMIT $3`, schema),
		workspaceID, query, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: keyword search: %w", err)
	}
	defer rows.Close()

	var out []domain.Chunk
	for rows.Next() {
		var c domain.Chunk
		if err := rows.Scan(&c.ID, &c.DocID, &c.Index, &c.Text); err != nil {
			return nil, fmt.Errorf("chunk_repo: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *ChunkRepo) DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error {
	schema := schemaFor(tenantID)
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM "%s".knowledge_chunks WHERE workspace_id = $1`, schema),
		workspaceID,
	)
	if err != nil {
		return fmt.Errorf("chunk_repo: delete by workspace: %w", err)
	}
	return nil
}
