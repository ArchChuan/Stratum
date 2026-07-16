package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const redacted = "[REDACTED]"

var sensitiveText = regexp.MustCompile(`(?i)\b(password|token|api_key|apikey|authorization|secret)=((bearer|basic)\s+)?\S+`)

type PgRunRepository struct {
	pool *pgxpool.Pool
}

func NewPgRunRepository(pool *pgxpool.Pool) *PgRunRepository {
	return &PgRunRepository{pool: pool}
}

func (r *PgRunRepository) SaveRun(ctx context.Context, tenantID string, run domain.EvalRun) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO eval_runs
			 (id, resource_kind, resource_id, revision_id, suite_revision_id, status, passed,
			  total_cases, passed_cases, metrics, created_at, started_at, completed_at)
			 VALUES ($1,$2,$3,$4,$5,'succeeded',$6,$7,$8,'{}',$9,$9,NOW())`,
			run.ID, string(run.Resource.Kind), run.Resource.ResourceID, run.Resource.RevisionID,
			run.SuiteRevisionID, run.Passed, run.TotalCases, run.PassedCases, run.CreatedAt,
		); err != nil {
			return fmt.Errorf("evaluation run repository: insert run: %w", err)
		}
		for _, result := range run.Results {
			actualJSON, err := json.Marshal(sanitizeValue(result.Actual))
			if err != nil {
				return fmt.Errorf("evaluation run repository: marshal actual output: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO eval_case_results
				 (id, run_id, case_id, passed, actual_output, message, error_message, trace_id,
				  tokens, cost_usd, duration_ms)
				 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11)`,
				uuid.Must(uuid.NewV7()).String(), run.ID, result.CaseID, result.Passed, string(actualJSON),
				result.Message, result.Error, result.TraceID, result.Tokens, result.CostUSD, result.DurationMs,
			); err != nil {
				return fmt.Errorf("evaluation run repository: insert case result: %w", err)
			}
		}
		return nil
	})
}

func (r *PgRunRepository) GetRun(
	ctx context.Context,
	tenantID, runID string,
) (domain.EvalRun, bool, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var run domain.EvalRun
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, revision_id, suite_revision_id,
			        passed, total_cases, passed_cases, created_at
			 FROM eval_runs WHERE id=$1`, runID,
		).Scan(&run.ID, &kind, &run.Resource.ResourceID, &run.Resource.RevisionID,
			&run.SuiteRevisionID, &run.Passed, &run.TotalCases, &run.PassedCases, &run.CreatedAt)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		run.Resource.Kind = domain.ResourceKind(kind)
		rows, err := tx.Query(ctx,
			`SELECT case_id, passed, actual_output, message, error_message, trace_id, tokens, cost_usd, duration_ms
			 FROM eval_case_results WHERE run_id=$1 ORDER BY created_at, id`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var result domain.EvalCaseResult
			var actualJSON []byte
			if err := rows.Scan(&result.CaseID, &result.Passed, &actualJSON, &result.Message, &result.Error,
				&result.TraceID, &result.Tokens, &result.CostUSD, &result.DurationMs); err != nil {
				return err
			}
			_ = json.Unmarshal(actualJSON, &result.Actual)
			run.Results = append(run.Results, result)
		}
		return rows.Err()
	})
	return run, found, err
}

func sanitizeValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSensitiveKey(key) {
				out[key] = redacted
				continue
			}
			out[key] = sanitizeValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeValue(item)
		}
		return out
	case string:
		return sensitiveText.ReplaceAllString(v, "$1="+redacted)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "password", "token", "api_key", "apikey", "authorization", "secret", "access_token", "refresh_token":
		return true
	default:
		return false
	}
}
