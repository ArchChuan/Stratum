package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
)

const observationColumns = "id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at"

// PgObservationRepository 实现 port.ObservationRepository（tenant-scoped）。
type PgObservationRepository struct {
	pool poolIface
}

// 编译期断言：PgObservationRepository 满足 port.ObservationRepository。
var _ port.ObservationRepository = (*PgObservationRepository)(nil)

func NewPgObservationRepository(pool poolIface) *PgObservationRepository {
	return &PgObservationRepository{pool: pool}
}

// execTenantTx 是 pool_iface.go 里唯一的事务执行器，签名：
// func execTenantTx(ctx context.Context, pool poolIface, tenantID string,
//
//	fn func(context.Context, pgx.Tx) error) error
//
// 所有方法先 postgres.WithTenant 设租户上下文再进事务，读写都在事务内完成
// （与 PgRunRepository 完全一致；该包不存在 execTenant/execTenantQuery helper）。

func (r *PgObservationRepository) Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error {
	if err := obs.Validate(); err != nil {
		return err
	}
	paramJSON, err := json.Marshal(obs.Param)
	if err != nil {
		return fmt.Errorf("marshal param_version: %w", err)
	}
	signalsJSON, err := json.Marshal(obs.Signals)
	if err != nil {
		return fmt.Errorf("marshal signals: %w", err)
	}
	costJSON, err := json.Marshal(obs.CostPerf)
	if err != nil {
		return fmt.Errorf("marshal cost_perf: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO eval_observations (id, trace_id, resource_kind, resource_id, param_version, signals, cost_perf, stratum, verdict, created_at)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			obs.ID, obs.TraceID, string(obs.Resource.Kind), obs.Resource.ResourceID,
			string(paramJSON), string(signalsJSON), string(costJSON),
			obs.Stratum, string(obs.Verdict), obs.CreatedAt,
		)
		if execErr != nil {
			return fmt.Errorf("insert eval observation: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("save eval observation: %w", err)
	}
	return nil
}

func (r *PgObservationRepository) Get(ctx context.Context, tenantID, observationID string) (*domain.EvalObservation, error) {
	var (
		obs                              domain.EvalObservation
		kind, verdict                    string
		paramJSON, signalsJSON, costJSON string
	)
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT `+observationColumns+` FROM eval_observations WHERE id = $1`, observationID,
		).Scan(&obs.ID, &obs.TraceID, &kind, &obs.Resource.ResourceID,
			&paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &verdict, &obs.CreatedAt)
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // 未找到：handler 依 obs==nil 返回 404
		}
		return nil, fmt.Errorf("get eval observation %s: %w", observationID, err)
	}
	obs.Resource.Kind = domain.ResourceKind(kind)
	obs.Verdict = domain.ObservationVerdict(verdict)
	if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
		return nil, fmt.Errorf("get eval observation %s: unmarshal: %w", observationID, err)
	}
	return &obs, nil
}

func (r *PgObservationRepository) QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
	from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
	query := `SELECT ` + observationColumns + ` FROM eval_observations WHERE resource_kind = $1 AND resource_id = $2`
	args := []any{resourceKind, resourceID}
	if from != nil {
		args = append(args, *from)
		query += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	if to != nil {
		args = append(args, *to)
		query += fmt.Sprintf(" AND created_at <= $%d", len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	var out []domain.EvalObservation
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				obs                              domain.EvalObservation
				kind, verdict                    string
				paramJSON, signalsJSON, costJSON string
			)
			if err := rows.Scan(&obs.ID, &obs.TraceID, &kind, &obs.Resource.ResourceID,
				&paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &verdict, &obs.CreatedAt); err != nil {
				return err
			}
			obs.Resource.Kind = domain.ResourceKind(kind)
			obs.Verdict = domain.ObservationVerdict(verdict)
			if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
				return err
			}
			out = append(out, obs)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query eval observations: %w", err)
	}
	return out, nil
}

func (r *PgObservationRepository) FindLatestByTrace(
	ctx context.Context, tenantID, traceID string,
) (*domain.EvalObservation, error) {
	var (
		obs                              domain.EvalObservation
		kind, verdict                    string
		paramJSON, signalsJSON, costJSON string
	)
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT `+observationColumns+` FROM eval_observations WHERE trace_id = $1 ORDER BY created_at DESC LIMIT 1`,
			traceID,
		).Scan(&obs.ID, &obs.TraceID, &kind, &obs.Resource.ResourceID,
			&paramJSON, &signalsJSON, &costJSON, &obs.Stratum, &verdict, &obs.CreatedAt)
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("find latest observation by trace: %w", err)
	}
	obs.Resource.Kind = domain.ResourceKind(kind)
	obs.Verdict = domain.ObservationVerdict(verdict)
	if err := unmarshalObservationJSON(&obs, paramJSON, signalsJSON, costJSON); err != nil {
		return nil, fmt.Errorf("find latest observation by trace: unmarshal: %w", err)
	}
	return &obs, nil
}

func (r *PgObservationRepository) UpdateBehaviorSignals(
	ctx context.Context, tenantID, observationID string, signals domain.BehaviorSignals,
) error {
	signalsJSON, err := json.Marshal(signals)
	if err != nil {
		return fmt.Errorf("marshal behavior signals: %w", err)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	err = execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`UPDATE eval_observations SET signals = jsonb_set(signals, '{behavior}', $2) WHERE id = $1`,
			observationID, string(signalsJSON),
		)
		if execErr != nil {
			return fmt.Errorf("update behavior signals: %w", execErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update behavior signals: %w", err)
	}
	return nil
}

func unmarshalObservationJSON(obs *domain.EvalObservation, paramJSON, signalsJSON, costJSON string) error {
	if err := json.Unmarshal([]byte(paramJSON), &obs.Param); err != nil {
		return fmt.Errorf("decode param_version: %w", err)
	}
	if err := json.Unmarshal([]byte(signalsJSON), &obs.Signals); err != nil {
		return fmt.Errorf("decode signals: %w", err)
	}
	if err := json.Unmarshal([]byte(costJSON), &obs.CostPerf); err != nil {
		return fmt.Errorf("decode cost_perf: %w", err)
	}
	return nil
}
