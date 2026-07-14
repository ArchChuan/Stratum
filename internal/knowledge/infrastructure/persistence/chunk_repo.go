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
	schema := schemaFor(tenantID)
	batch := &pgx.Batch{}
	for _, c := range chunks {
		var parentID any = nil
		if c.ParentID != "" {
			parentID = c.ParentID
		}
		batch.Queue(
			fmt.Sprintf(`INSERT INTO "%s".knowledge_chunks(id, workspace_id, doc_id, chunk_index, content, parent_id)
				VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, schema),
			c.ID, workspaceID, c.DocID, c.Index, c.Text, parentID,
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

func (r *ChunkRepo) InsertParentBatch(ctx context.Context, tenantID, workspaceID string, parents []port.ParentChunk) error {
	if len(parents) == 0 {
		return nil
	}
	schema := schemaFor(tenantID)
	batch := &pgx.Batch{}
	for _, p := range parents {
		batch.Queue(
			fmt.Sprintf(`INSERT INTO "%s".knowledge_parent_chunks(id, workspace_id, doc_id, chunk_index, content)
				VALUES ($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, schema),
			p.ID, workspaceID, p.DocID, p.Index, p.Content,
		)
	}
	br := r.db.SendBatch(ctx, batch)
	defer br.Close() //nolint:errcheck
	for range parents {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("chunk_repo: insert parent: %w", err)
		}
	}
	return nil
}

func (r *ChunkRepo) GetParentByID(ctx context.Context, tenantID, workspaceID, parentID string) (*port.ParentChunk, error) {
	schema := schemaFor(tenantID)
	var p port.ParentChunk
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id, workspace_id, doc_id, chunk_index, content
			FROM "%s".knowledge_parent_chunks
			WHERE id = $1 AND workspace_id = $2`, schema),
		parentID, workspaceID,
	).Scan(&p.ID, &p.WorkspaceID, &p.DocID, &p.Index, &p.Content)
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: get parent: %w", err)
	}
	return &p, nil
}

func (r *ChunkRepo) GetChunksByIDs(ctx context.Context, tenantID, workspaceID string, ids []string) ([]domain.Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	schema := schemaFor(tenantID)
	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, doc_id, chunk_index, content, parent_id
			FROM "%s".knowledge_chunks
			WHERE workspace_id = $1 AND id = ANY($2)`, schema),
		workspaceID, ids,
	)
	if err != nil {
		return nil, fmt.Errorf("chunk_repo: get by ids: %w", err)
	}
	defer rows.Close()

	var out []domain.Chunk
	for rows.Next() {
		var c domain.Chunk
		var parentID *string
		if err := rows.Scan(&c.ID, &c.DocID, &c.Index, &c.Text, &parentID); err != nil {
			return nil, fmt.Errorf("chunk_repo: scan: %w", err)
		}
		if parentID != nil {
			c.ParentID = *parentID
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *ChunkRepo) KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error) {
	schema := schemaFor(tenantID)
	rows, err := r.db.Query(ctx,
		// Must use the same text-search config as the GENERATED tsv column
		// (public.chinese_zh) or the GIN index on tsv is bypassed and the query
		// vs document tokenizations disagree. chinese_zh is zhparser when the
		// extension is installed, otherwise a copy of 'simple' (see public_schema.sql).
		fmt.Sprintf(`SELECT id, doc_id, chunk_index, content
			FROM "%s".knowledge_chunks
			WHERE workspace_id = $1
			  AND tsv @@ plainto_tsquery('public.chinese_zh', $2)
			ORDER BY ts_rank(tsv, plainto_tsquery('public.chinese_zh', $2)) DESC
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
	// Leaf chunks first: their parent_id FK is ON DELETE SET NULL, so deleting
	// leaves does not cascade to parents. Both tables must be purged explicitly
	// because DeleteWorkspaceData also serves the "clear data, keep workspace"
	// path where the rag_workspaces row (and its ON DELETE CASCADE) is untouched.
	if _, err := r.db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM "%s".knowledge_chunks WHERE workspace_id = $1`, schema),
		workspaceID,
	); err != nil {
		return fmt.Errorf("chunk_repo: delete by workspace: %w", err)
	}
	if _, err := r.db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM "%s".knowledge_parent_chunks WHERE workspace_id = $1`, schema),
		workspaceID,
	); err != nil {
		return fmt.Errorf("chunk_repo: delete parents by workspace: %w", err)
	}
	return nil
}
