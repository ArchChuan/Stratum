package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
)

// pgxPool 是 pgx 连接池的最小接口（公共 schema 数据，不走租户边界封装）。
type pgxPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ProfileRepo 持久化 model_profiles（public schema，global 共享）。
type ProfileRepo struct {
	db pgxPool
}

// NewProfileRepo 创建 ProfileRepo。
func NewProfileRepo(db pgxPool) *ProfileRepo {
	return &ProfileRepo{db: db}
}

// GetByFamilyKey 按族键读取档案；不存在时 ok=false。
func (r *ProfileRepo) GetByFamilyKey(ctx context.Context, familyKey string) (domain.Profile, bool, error) {
	row := r.db.QueryRow(ctx,
		`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at
		 FROM model_profiles WHERE family_key=$1`, familyKey)
	p, err := scanProfile(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Profile{}, false, nil
		}
		return domain.Profile{}, false, fmt.Errorf("profile_repo: get by family key: %w", err)
	}
	return p, true, nil
}

// List 返回全部档案（行数个位数，匹配在应用层做）。
func (r *ProfileRepo) List(ctx context.Context) ([]domain.Profile, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by, created_at, updated_at
		 FROM model_profiles ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("profile_repo: list: %w", err)
	}
	defer rows.Close()

	profiles := make([]domain.Profile, 0, 8)
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("profile_repo: list scan: %w", err)
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("profile_repo: list iterate: %w", err)
	}
	return profiles, nil
}

// Upsert 幂等写入档案（族键冲突即覆盖）。
func (r *ProfileRepo) Upsert(ctx context.Context, p domain.Profile) error {
	matcherJSON, err := json.Marshal(p.Matcher)
	if err != nil {
		return fmt.Errorf("profile_repo: marshal matcher: %w", err)
	}
	baselineJSON, err := json.Marshal(p.Baseline)
	if err != nil {
		return fmt.Errorf("profile_repo: marshal baseline: %w", err)
	}
	_, err = r.db.Exec(ctx,
		`INSERT INTO model_profiles (id, family_key, display_name, model_matcher, baseline, fingerprint, version, status, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (family_key) DO UPDATE SET
		   display_name=EXCLUDED.display_name, model_matcher=EXCLUDED.model_matcher,
		   baseline=EXCLUDED.baseline, fingerprint=EXCLUDED.fingerprint,
		   version=model_profiles.version+1, status=EXCLUDED.status,
		   // created_by 在冲突覆盖时更新为最后操作者（版本化语义，非原始创建者）。
		   created_by=EXCLUDED.created_by, updated_at=NOW()`,
		p.ID, p.FamilyKey, p.DisplayName, string(matcherJSON), string(baselineJSON),
		p.Fingerprint, p.Version, p.Status, p.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("profile_repo: upsert %s: %w", p.FamilyKey, err)
	}
	return nil
}

// rowScanner 兼容 pgx.Row 与 pgx.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProfile(row rowScanner) (domain.Profile, error) {
	var p domain.Profile
	var matcherJSON, baselineJSON []byte
	if err := row.Scan(
		&p.ID, &p.FamilyKey, &p.DisplayName, &matcherJSON, &baselineJSON,
		&p.Fingerprint, &p.Version, &p.Status, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return domain.Profile{}, err
	}
	if err := json.Unmarshal(matcherJSON, &p.Matcher); err != nil {
		return domain.Profile{}, fmt.Errorf("profile_repo: decode matcher: %w", err)
	}
	if err := json.Unmarshal(baselineJSON, &p.Baseline); err != nil {
		return domain.Profile{}, fmt.Errorf("profile_repo: decode baseline: %w", err)
	}
	return p, nil
}
