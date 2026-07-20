package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
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
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, c := range chunks {
			var parentID any
			if c.ParentID != "" {
				parentID = c.ParentID
			}
			batch.Queue(`INSERT INTO knowledge_chunks(id, workspace_id, doc_id, chunk_index, content, parent_id)
				VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`,
				c.ID, workspaceID, c.DocID, c.Index, c.Text, parentID)
		}
		br := tx.SendBatch(ctx, batch)
		defer br.Close() //nolint:errcheck
		for range chunks {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("chunk_repo: insert: %w", err)
			}
		}
		return nil
	})
}

func (r *ChunkRepo) InsertParentBatch(ctx context.Context, tenantID, workspaceID string, parents []port.ParentChunk) error {
	if len(parents) == 0 {
		return nil
	}
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, p := range parents {
			batch.Queue(`INSERT INTO knowledge_parent_chunks(id, workspace_id, doc_id, chunk_index, content)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, p.ID, workspaceID, p.DocID, p.Index, p.Content)
		}
		br := tx.SendBatch(ctx, batch)
		defer br.Close() //nolint:errcheck
		for range parents {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("chunk_repo: insert parent: %w", err)
			}
		}
		return nil
	})
}

func (r *ChunkRepo) GetParentByID(ctx context.Context, tenantID, workspaceID, parentID string) (*port.ParentChunk, error) {
	var p port.ParentChunk
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, workspace_id, doc_id, chunk_index, content
			FROM knowledge_parent_chunks WHERE id = $1 AND workspace_id = $2`, parentID, workspaceID,
		).Scan(&p.ID, &p.WorkspaceID, &p.DocID, &p.Index, &p.Content)
	})
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: get parent: %w", err)
	}
	return &p, nil
}

func (r *ChunkRepo) GetChunksByIDs(ctx context.Context, tenantID, workspaceID string, ids []string) ([]domain.Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []domain.Chunk
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, doc_id, chunk_index, content, parent_id
			FROM knowledge_chunks WHERE workspace_id = $1 AND id = ANY($2)`, workspaceID, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.Chunk
			var parentID *string
			if err := rows.Scan(&c.ID, &c.DocID, &c.Index, &c.Text, &parentID); err != nil {
				return fmt.Errorf("chunk_repo: scan: %w", err)
			}
			if parentID != nil {
				c.ParentID = *parentID
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: get by ids: %w", err)
	}
	return out, nil
}

func (r *ChunkRepo) KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error) {
	var out []domain.Chunk
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			// Must use the same text-search config as the GENERATED tsv column
			// (public.chinese_zh) or the GIN index on tsv is bypassed and the query
			// vs document tokenizations disagree. chinese_zh is zhparser when the
			// extension is installed, otherwise a copy of 'simple' (see public_schema.sql).
			`SELECT id, doc_id, chunk_index, content
			FROM knowledge_chunks
			WHERE workspace_id = $1
			  AND tsv @@ plainto_tsquery('public.chinese_zh', $2)
			ORDER BY ts_rank(tsv, plainto_tsquery('public.chinese_zh', $2)) DESC
			LIMIT $3`, workspaceID, query, topK)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c domain.Chunk
			if err := rows.Scan(&c.ID, &c.DocID, &c.Index, &c.Text); err != nil {
				return fmt.Errorf("chunk_repo: scan: %w", err)
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: keyword search: %w", err)
	}
	return out, nil
}

func (r *ChunkRepo) DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error {
	// Leaf chunks first: their parent_id FK is ON DELETE SET NULL, so deleting
	// leaves does not cascade to parents. Both tables must be purged explicitly
	// because DeleteWorkspaceData also serves the "clear data, keep workspace"
	// path where the rag_workspaces row (and its ON DELETE CASCADE) is untouched.
	return execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM knowledge_chunks WHERE workspace_id = $1`, workspaceID); err != nil {
			return fmt.Errorf("chunk_repo: delete by workspace: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM knowledge_parent_chunks WHERE workspace_id = $1`, workspaceID); err != nil {
			return fmt.Errorf("chunk_repo: delete parents by workspace: %w", err)
		}
		return nil
	})
}
