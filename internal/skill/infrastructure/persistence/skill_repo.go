// Package persistence — Postgres adapter for skill rows. Implements
// port.SkillRepo using per-tenant schema search_path.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgSkillRepo persists skill rows in PostgreSQL under per-tenant schemas.
type PgSkillRepo struct {
	pool *pgxpool.Pool
}

// NewPgSkillRepo constructs a Postgres-backed SkillRepo.
func NewPgSkillRepo(pool *pgxpool.Pool) *PgSkillRepo {
	return &PgSkillRepo{pool: pool}
}

func (r *PgSkillRepo) Insert(ctx context.Context, row port.SkillRow) (time.Time, error) {
	cfgJSON, err := json.Marshal(row.Config)
	if err != nil {
		return time.Time{}, fmt.Errorf("skill_repo: marshal config: %w", err)
	}
	var createdAt time.Time
	err = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO skills (id, name, description, type, config)
			 VALUES ($1, $2, $3, $4, $5)
			 RETURNING created_at`,
			row.ID, row.Name, row.Description, row.Type, string(cfgJSON),
		).Scan(&createdAt)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return time.Time{}, domain.ErrSkillNameConflict
		}
		return time.Time{}, err
	}
	return createdAt, nil
}

func (r *PgSkillRepo) Get(ctx context.Context, id string) (port.SkillRow, bool, error) {
	var row port.SkillRow
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var cfgJSON []byte
		err := tx.QueryRow(ctx,
			`SELECT id, name, description, type, config, created_at FROM skills WHERE id=$1`, id,
		).Scan(&row.ID, &row.Name, &row.Description, &row.Type, &cfgJSON, &row.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return json.Unmarshal(cfgJSON, &row.Config)
	})
	if err != nil {
		return port.SkillRow{}, false, err
	}
	if !found {
		return port.SkillRow{}, false, domain.ErrSkillNotFound
	}
	return row, true, nil
}

func (r *PgSkillRepo) List(ctx context.Context) ([]port.SkillRow, error) {
	var rows []port.SkillRow
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		pgRows, err := tx.Query(ctx, `SELECT id, name, description, type, config, created_at FROM skills ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer pgRows.Close()
		for pgRows.Next() {
			var rRow port.SkillRow
			var cfgJSON []byte
			if err := pgRows.Scan(&rRow.ID, &rRow.Name, &rRow.Description, &rRow.Type, &cfgJSON, &rRow.CreatedAt); err != nil {
				return err
			}
			_ = json.Unmarshal(cfgJSON, &rRow.Config)
			rows = append(rows, rRow)
		}
		return pgRows.Err()
	})
	return rows, err
}

func (r *PgSkillRepo) GetType(ctx context.Context, id string) (string, error) {
	var typ string
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT type FROM skills WHERE id=$1`, id).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrSkillNotFound
		}
		return err
	})
	return typ, err
}

func (r *PgSkillRepo) Update(ctx context.Context, row port.SkillRow) (time.Time, error) {
	cfgJSON, err := json.Marshal(row.Config)
	if err != nil {
		return time.Time{}, fmt.Errorf("skill_repo: marshal config: %w", err)
	}
	var createdAt time.Time
	err = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE skills SET name=$2, description=$3, type=$4, config=$5 WHERE id=$1 RETURNING created_at`,
			row.ID, row.Name, row.Description, row.Type, string(cfgJSON),
		).Scan(&createdAt)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return time.Time{}, domain.ErrSkillNameConflict
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, domain.ErrSkillNotFound
		}
		return time.Time{}, err
	}
	return createdAt, nil
}

func (r *PgSkillRepo) Delete(ctx context.Context, id string) error {
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrSkillNotFound
		}
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.ErrSkillLinked
		}
		return err
	}
	return nil
}

func (r *PgSkillRepo) GetTypeAndConfig(ctx context.Context, id string) (string, map[string]any, error) {
	var typ string
	var cfgJSON []byte
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT type, config FROM skills WHERE id=$1`, id).Scan(&typ, &cfgJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrSkillNotFound
		}
		return err
	})
	if err != nil {
		return "", nil, err
	}
	var cfg map[string]any
	_ = json.Unmarshal(cfgJSON, &cfg)
	return typ, cfg, nil
}
