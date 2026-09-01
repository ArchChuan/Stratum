package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const redacted = "[REDACTED]"

var sensitiveText = regexp.MustCompile(`(?i)\b(password|token|api_key|apikey|authorization|secret)=((bearer|basic)\s+)?\S+`)

type PgRunRepository struct {
	pool poolIface
}

func NewPgRunRepository(pool *pgxpool.Pool) *PgRunRepository {
	return &PgRunRepository{pool: pool}
}

func (r *PgRunRepository) SaveRun(ctx context.Context, tenantID string, run domain.EvalRun) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	metricsJSON, err := json.Marshal(run.Metrics)
	if err != nil {
		return fmt.Errorf("evaluation run repository: marshal metrics: %w", err)
	}
	if string(metricsJSON) == "null" {
		metricsJSON = []byte("{}")
	}
	return execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO eval_runs
			 (id, resource_kind, resource_id, revision_id, suite_revision_id, status, passed,
			  total_cases, passed_cases, metrics, created_at, started_at, completed_at)
			 VALUES ($1,$2,$3,$4,$5,'succeeded',$6,$7,$8,$9,$10,$10,NOW())`,
			run.ID, string(run.Resource.Kind), run.Resource.ResourceID, run.Resource.RevisionID,
			run.SuiteRevisionID, run.Passed, run.TotalCases, run.PassedCases, string(metricsJSON), run.CreatedAt,
		); err != nil {
			return fmt.Errorf("evaluation run repository: insert run: %w", err)
		}
		for _, result := range run.Results {
			if err := insertCaseResult(ctx, tx, run.ID, result); err != nil {
				return err
			}
		}
		return nil
	})
}

// insertCaseResult 在事务内写入一条 eval_case_results（spec §6.2/§6.5）：
// actual/dimensions/trace_evidence/tool_sequence 均 JSON 序列化，工具序列落库前
// 经 sanitizeTools 脱敏；JSON-null 分别回退到 {} / [] 保持 round-trip 语义。
func insertCaseResult(ctx context.Context, tx pgx.Tx, runID string, result domain.EvalCaseResult) error {
	id := result.ID
	if id == "" {
		id = uuid.Must(uuid.NewV7()).String()
	}
	actualJSON, err := json.Marshal(sanitizeValue(result.Actual))
	if err != nil {
		return fmt.Errorf("evaluation run repository: marshal actual output: %w", err)
	}
	dimensionsJSON, err := json.Marshal(result.Dimensions)
	if err != nil {
		return fmt.Errorf("evaluation run repository: marshal dimensions: %w", err)
	}
	if string(dimensionsJSON) == "null" {
		dimensionsJSON = []byte("[]")
	}
	traceJSON, err := json.Marshal(result.TraceEvidence)
	if err != nil {
		return fmt.Errorf("evaluation run repository: marshal trace evidence: %w", err)
	}
	toolSequenceJSON, err := json.Marshal(sanitizeTools(result.Tools))
	if err != nil {
		return fmt.Errorf("evaluation run repository: marshal tool sequence: %w", err)
	}
	if string(toolSequenceJSON) == "null" {
		toolSequenceJSON = []byte("[]")
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO eval_case_results
		 (id, run_id, case_id, passed, actual_output, message, error_message, trace_id,
		  tokens, cost_usd, duration_ms, dimensions, failure_reason, trace_evidence,
		  process_pass, process_failure, tool_sequence)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		id, runID, result.CaseID, result.Passed, string(actualJSON),
		result.Message, result.Error, result.TraceID, result.Tokens, result.CostUSD, result.DurationMs,
		string(dimensionsJSON), result.FailureReason, string(traceJSON),
		result.ProcessPass, result.ProcessFailure, string(toolSequenceJSON),
	); err != nil {
		return fmt.Errorf("evaluation run repository: insert case result: %w", err)
	}
	return nil
}

func (r *PgRunRepository) GetRun(
	ctx context.Context,
	tenantID, runID string,
) (domain.EvalRun, bool, error) {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	var run domain.EvalRun
	found := false
	err := execTenantTx(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var kind string
		var metricsJSON []byte
		err := tx.QueryRow(ctx,
			`SELECT id, resource_kind, resource_id, revision_id, suite_revision_id,
			        passed, total_cases, passed_cases, metrics, created_at
			 FROM eval_runs WHERE id=$1`, runID,
		).Scan(&run.ID, &kind, &run.Resource.ResourceID, &run.Resource.RevisionID,
			&run.SuiteRevisionID, &run.Passed, &run.TotalCases, &run.PassedCases, &metricsJSON, &run.CreatedAt)
		if err == nil && len(metricsJSON) > 0 {
			_ = json.Unmarshal(metricsJSON, &run.Metrics)
		}
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		run.Resource.Kind = domain.ResourceKind(kind)
		rows, err := tx.Query(ctx,
			`SELECT case_id, passed, actual_output, message, error_message, trace_id, tokens, cost_usd,
			        duration_ms, dimensions, failure_reason, trace_evidence,
			        process_pass, process_failure, tool_sequence
			 FROM eval_case_results WHERE run_id=$1 ORDER BY created_at, id`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			result, err := scanCaseResult(rows)
			if err != nil {
				return err
			}
			run.Results = append(run.Results, result)
		}
		return rows.Err()
	})
	return run, found, err
}

// scanCaseResult 从一行 eval_case_results 读出 case 结果，并把 JSON 列反序列化
// 到对应结构体字段（spec §6.2/§6.5）。
func scanCaseResult(row pgx.Row) (domain.EvalCaseResult, error) {
	var result domain.EvalCaseResult
	var actualJSON []byte
	var dimensionsJSON []byte
	var traceJSON []byte
	var toolSequenceJSON []byte
	if err := row.Scan(&result.CaseID, &result.Passed, &actualJSON, &result.Message, &result.Error,
		&result.TraceID, &result.Tokens, &result.CostUSD, &result.DurationMs,
		&dimensionsJSON, &result.FailureReason, &traceJSON,
		&result.ProcessPass, &result.ProcessFailure, &toolSequenceJSON); err != nil {
		return result, err
	}
	_ = json.Unmarshal(actualJSON, &result.Actual)
	if len(dimensionsJSON) > 0 {
		_ = json.Unmarshal(dimensionsJSON, &result.Dimensions)
	}
	if len(traceJSON) > 0 {
		_ = json.Unmarshal(traceJSON, &result.TraceEvidence)
	}
	if len(toolSequenceJSON) > 0 {
		_ = json.Unmarshal(toolSequenceJSON, &result.Tools)
	}
	return result, nil
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

// sanitizeTools 落库前脱敏工具序列（spec §6.5）：Arguments 复用 sanitizeValue
// 递归脱敏（敏感 key 与内嵌键值），RawText 用 sensitiveText 正则替换敏感键值对；
// 其余字段原样透传。返回新切片，不修改入参。
func sanitizeTools(tools []domain.ToolObservation) []domain.ToolObservation {
	if len(tools) == 0 {
		return tools
	}
	out := make([]domain.ToolObservation, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if tool.Arguments != nil {
			if args, ok := sanitizeValue(tool.Arguments).(map[string]any); ok {
				out[i].Arguments = args
			}
		}
		out[i].RawText = sensitiveText.ReplaceAllString(tool.RawText, "$1="+redacted)
	}
	return out
}
