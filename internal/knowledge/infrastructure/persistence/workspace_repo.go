package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// WorkspaceRepo persists knowledge workspaces in per-tenant schemas.
type WorkspaceRepo struct {
	db *pgxpool.Pool
}

// NewWorkspaceRepo constructs a WorkspaceRepo backed by the given pool.
func NewWorkspaceRepo(db *pgxpool.Pool) *WorkspaceRepo {
	return &WorkspaceRepo{db: db}
}

// jsonbConfig matches the JSONB shape stored in rag_workspaces.config.
type jsonbConfig struct {
	EmbeddingModel string `json:"embedding_model"`
	ChunkSize      int    `json:"chunk_size"`
	ChunkOverlap   int    `json:"chunk_overlap"`
	QueryMode      string `json:"query_mode"`
	TopK           int    `json:"top_k"`
}

func toJSONB(c domain.WorkspaceConfig) jsonbConfig {
	return jsonbConfig{
		EmbeddingModel: c.EmbeddingModel,
		ChunkSize:      c.ChunkSize,
		ChunkOverlap:   c.ChunkOverlap,
		QueryMode:      c.QueryMode,
		TopK:           c.TopK,
	}
}

func fromJSONB(c jsonbConfig) domain.WorkspaceConfig {
	return domain.WorkspaceConfig{
		EmbeddingModel: c.EmbeddingModel,
		ChunkSize:      c.ChunkSize,
		ChunkOverlap:   c.ChunkOverlap,
		QueryMode:      c.QueryMode,
		TopK:           c.TopK,
	}
}

func schemaFor(tenantID string) string {
	return "tenant_" + tenantID
}

// Create inserts a workspace, returning ErrWorkspaceConflict on unique violation.
func (r *WorkspaceRepo) Create(ctx context.Context, tenantID string, ws *domain.Workspace) error {
	schema := schemaFor(tenantID)
	var id string
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`INSERT INTO "%s".rag_workspaces (name, description, config)
                     VALUES ($1, $2, $3) RETURNING id`, schema),
		ws.Name, ws.Description, toJSONB(ws.Config),
	).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return domain.ErrWorkspaceConflict
		}
		return fmt.Errorf("workspace_repo: create: %w", err)
	}
	ws.ID = id
	return nil
}

// GetByName returns a workspace by name; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	schema := schemaFor(tenantID)
	var (
		ws domain.Workspace
		jc jsonbConfig
	)
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	).Scan(&ws.ID, &ws.Name, &ws.Description, &jc, &ws.CreatedAt, &ws.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("workspace_repo: get by name: %w", err)
	}
	ws.Config = fromJSONB(jc)
	return &ws, nil
}

// List returns workspaces ordered by created_at DESC.
func (r *WorkspaceRepo) List(ctx context.Context, tenantID string) ([]*domain.Workspace, error) {
	schema := schemaFor(tenantID)
	rows, err := r.db.Query(ctx,
		fmt.Sprintf(`SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM "%s".rag_workspaces ORDER BY created_at DESC`, schema),
	)
	if err != nil {
		return nil, fmt.Errorf("workspace_repo: list: %w", err)
	}
	defer rows.Close()

	var out []*domain.Workspace
	for rows.Next() {
		var (
			ws domain.Workspace
			jc jsonbConfig
		)
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Description, &jc, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
			return nil, fmt.Errorf("workspace_repo: scan list row: %w", err)
		}
		ws.Config = fromJSONB(jc)
		out = append(out, &ws)
	}
	return out, nil
}

// UpdateDescriptionAndConfig overwrites description (when non-nil) and config.
func (r *WorkspaceRepo) UpdateDescriptionAndConfig(ctx context.Context, tenantID, name string, description *string, cfg domain.WorkspaceConfig) error {
	schema := schemaFor(tenantID)
	_, err := r.db.Exec(ctx,
		fmt.Sprintf(`UPDATE "%s".rag_workspaces
                     SET description = COALESCE($1, description),
                         config = $2,
                         updated_at = NOW()
                     WHERE name = $3`, schema),
		description, toJSONB(cfg), name,
	)
	if err != nil {
		return fmt.Errorf("workspace_repo: update: %w", err)
	}
	return nil
}

// Delete removes the workspace; ErrWorkspaceLinked on FK violation, ErrWorkspaceNotFound on 0 rows.
func (r *WorkspaceRepo) Delete(ctx context.Context, tenantID, name string) error {
	schema := schemaFor(tenantID)
	tag, err := r.db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.ErrWorkspaceLinked
		}
		return fmt.Errorf("workspace_repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkspaceNotFound
	}
	return nil
}

// GetConfigForUpload returns just the config of a workspace; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error) {
	schema := schemaFor(tenantID)
	var jc jsonbConfig
	err := r.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT config FROM "%s".rag_workspaces WHERE name = $1`, schema),
		name,
	).Scan(&jc)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("workspace_repo: get config: %w", err)
	}
	return fromJSONB(jc), nil
}
