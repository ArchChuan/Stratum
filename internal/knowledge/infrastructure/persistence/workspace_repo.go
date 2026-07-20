package persistence

import (
	"context"
	"encoding/json"
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
	EmbeddingModel   string `json:"embedding_model"`
	ChunkSize        int    `json:"chunk_size"`
	ChunkOverlap     int    `json:"chunk_overlap"`
	QueryMode        string `json:"query_mode"`
	TopK             int    `json:"top_k"`
	ChunkingStrategy string `json:"chunking_strategy"`
}

func toJSONB(c domain.WorkspaceConfig) string {
	b, _ := json.Marshal(jsonbConfig{
		EmbeddingModel:   c.EmbeddingModel,
		ChunkSize:        c.ChunkSize,
		ChunkOverlap:     c.ChunkOverlap,
		QueryMode:        c.QueryMode,
		TopK:             c.TopK,
		ChunkingStrategy: c.ChunkingStrategy,
	})
	return string(b)
}

func fromJSONB(c jsonbConfig) domain.WorkspaceConfig {
	return domain.WorkspaceConfig{
		EmbeddingModel:   c.EmbeddingModel,
		ChunkSize:        c.ChunkSize,
		ChunkOverlap:     c.ChunkOverlap,
		QueryMode:        c.QueryMode,
		TopK:             c.TopK,
		ChunkingStrategy: c.ChunkingStrategy,
	}
}

// Create inserts a workspace, returning ErrWorkspaceConflict on unique violation.
func (r *WorkspaceRepo) Create(ctx context.Context, tenantID string, ws *domain.Workspace) error {
	var id string
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO rag_workspaces (name, description, config)
                     VALUES ($1, $2, $3) RETURNING id`,
			ws.Name, ws.Description, toJSONB(ws.Config),
		).Scan(&id)
	})
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
	var (
		ws domain.Workspace
		jc jsonbConfig
	)
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM rag_workspaces WHERE name = $1`, name,
		).Scan(&ws.ID, &ws.Name, &ws.Description, &jc, &ws.CreatedAt, &ws.UpdatedAt)
	})
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
	var out []*domain.Workspace
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, COALESCE(description,''), config, created_at, updated_at
                     FROM rag_workspaces ORDER BY created_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ws domain.Workspace
			var jc jsonbConfig
			if err := rows.Scan(&ws.ID, &ws.Name, &ws.Description, &jc, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
				return fmt.Errorf("workspace_repo: scan list row: %w", err)
			}
			ws.Config = fromJSONB(jc)
			out = append(out, &ws)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("workspace_repo: list: %w", err)
	}
	return out, nil
}

// UpdateDescriptionAndConfig overwrites description (when non-nil) and config.
func (r *WorkspaceRepo) UpdateDescriptionAndConfig(ctx context.Context, tenantID, name string, description *string, cfg domain.WorkspaceConfig) error {
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE rag_workspaces
                     SET description = COALESCE($1, description),
                         config = $2,
                         updated_at = NOW()
			WHERE name = $3`, description, toJSONB(cfg), name)
		return err
	})
	if err != nil {
		return fmt.Errorf("workspace_repo: update: %w", err)
	}
	return nil
}

// UpdateName renames a workspace; ErrWorkspaceNotFound on 0 rows, ErrWorkspaceConflict on duplicate.
func (r *WorkspaceRepo) UpdateName(ctx context.Context, tenantID, oldName, newName string) error {
	var tag pgconn.CommandTag
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		tag, err = tx.Exec(ctx, `UPDATE rag_workspaces SET name = $1, updated_at = NOW() WHERE name = $2`, newName, oldName)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrWorkspaceConflict
		}
		return fmt.Errorf("workspace_repo: update_name: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkspaceNotFound
	}
	return nil
}

func (r *WorkspaceRepo) Delete(ctx context.Context, tenantID, name string) error {
	var tag pgconn.CommandTag
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		tag, err = tx.Exec(ctx, `DELETE FROM rag_workspaces WHERE name = $1`, name)
		return err
	})
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
	var jc jsonbConfig
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT config FROM rag_workspaces WHERE name = $1`, name).Scan(&jc)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("workspace_repo: get config: %w", err)
	}
	return fromJSONB(jc), nil
}

// GetConfigByID returns just the config of a workspace resolved by UUID; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error) {
	var jc jsonbConfig
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT config FROM rag_workspaces WHERE id = $1::uuid`, id).Scan(&jc)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("workspace_repo: get config by id: %w", err)
	}
	return fromJSONB(jc), nil
}
