package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/jackc/pgx/v5"
)

const redactedValue = "[REDACTED]"

var sensitiveTextPattern = regexp.MustCompile(
	`(?i)\b(password|token|api_key|apikey|authorization|secret)=((bearer|basic)\s+)?\S+`,
)

type PgToolTraceStore struct {
	pool chatPoolIface
}

func NewPgToolTraceStore(pool chatPoolIface) *PgToolTraceStore {
	return &PgToolTraceStore{pool: pool}
}

func (s *PgToolTraceStore) InsertBatch(ctx context.Context, tenantID string, traces []domain.ToolObservation) error {
	if len(traces) == 0 {
		return nil
	}
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, tr := range traces {
			argsJSON, argsTruncated, err := marshalJSONTextLimit(redactJSONValue(tr.Arguments), constants.AgentToolTraceMaxRawJSONBytes)
			if err != nil {
				return fmt.Errorf("tool_trace_store: marshal arguments: %w", err)
			}
			rawJSON, rawJSONTruncated, err := marshalJSONTextLimit(redactJSONValue(tr.RawResult), constants.AgentToolTraceMaxRawJSONBytes)
			if err != nil {
				return fmt.Errorf("tool_trace_store: marshal raw result: %w", err)
			}
			metadataJSON, err := marshalJSONText(redactJSONValue(tr.Metadata))
			if err != nil {
				return fmt.Errorf("tool_trace_store: marshal metadata: %w", err)
			}
			rawText, rawTextTruncated := truncateString(redactSensitiveText(tr.RawText), constants.AgentToolTraceMaxRawTextBytes)
			if _, err := tx.Exec(ctx,
				`INSERT INTO agent_tool_traces
				 (trace_id, execution_id, conversation_id, agent_id, user_id, step_index,
				  tool_call_id, tool_name, tool_type, provider_type, provider_id, server_id, capability_id,
				  arguments_json, raw_result_json, raw_result_text, summary, status, error_message,
				  latency_ms, raw_truncated, metadata_json, started_at, ended_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
				tr.TraceID, tr.ExecutionID, tr.ConversationID, tr.AgentID, tr.UserID, tr.StepIndex,
				tr.ToolCallID, tr.ToolName, tr.ToolType, tr.ProviderType, tr.ProviderID, tr.ServerID, tr.CapabilityID,
				argsJSON, rawJSON,
				rawText, tr.Summary, tr.Status, tr.ErrorMessage, tr.LatencyMs,
				tr.RawTruncated || argsTruncated || rawJSONTruncated || rawTextTruncated,
				metadataJSON, nullableTime(tr.StartedAt), nullableTime(tr.EndedAt),
			); err != nil {
				return fmt.Errorf("tool_trace_store: insert: %w", err)
			}
		}
		return nil
	})
}

func (s *PgToolTraceStore) ListByTraceID(ctx context.Context, tenantID, traceID string) ([]domain.ToolObservation, error) {
	return s.list(ctx, tenantID,
		`SELECT id, trace_id, execution_id, conversation_id, agent_id, user_id, step_index,
		        tool_call_id, tool_name, tool_type, provider_type, provider_id, server_id, capability_id,
		        arguments_json, raw_result_json, raw_result_text, summary, status, error_message,
		        latency_ms, raw_truncated, metadata_json, started_at, ended_at, created_at
		   FROM agent_tool_traces
		  WHERE trace_id = $1
		  ORDER BY step_index ASC, created_at ASC`,
		traceID,
	)
}

func (s *PgToolTraceStore) ListByConversation(ctx context.Context, tenantID, conversationID string, limit int) ([]domain.ToolObservation, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.list(ctx, tenantID,
		`SELECT id, trace_id, execution_id, conversation_id, agent_id, user_id, step_index,
		        tool_call_id, tool_name, tool_type, provider_type, provider_id, server_id, capability_id,
		        arguments_json, raw_result_json, raw_result_text, summary, status, error_message,
		        latency_ms, raw_truncated, metadata_json, started_at, ended_at, created_at
		   FROM agent_tool_traces
		  WHERE conversation_id = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		conversationID, limit,
	)
}

func (s *PgToolTraceStore) list(ctx context.Context, tenantID, query string, args ...any) ([]domain.ToolObservation, error) {
	var out []domain.ToolObservation
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tr domain.ToolObservation
			var argsRaw, rawResult, metadataRaw json.RawMessage
			var startedAt, endedAt sql.NullTime
			if err := rows.Scan(
				&tr.ID, &tr.TraceID, &tr.ExecutionID, &tr.ConversationID, &tr.AgentID, &tr.UserID, &tr.StepIndex,
				&tr.ToolCallID, &tr.ToolName, &tr.ToolType, &tr.ProviderType, &tr.ProviderID, &tr.ServerID, &tr.CapabilityID,
				&argsRaw, &rawResult, &tr.RawText, &tr.Summary, &tr.Status, &tr.ErrorMessage,
				&tr.LatencyMs, &tr.RawTruncated, &metadataRaw, &startedAt, &endedAt, &tr.CreatedAt,
			); err != nil {
				return err
			}
			_ = json.Unmarshal(argsRaw, &tr.Arguments)
			_ = json.Unmarshal(metadataRaw, &tr.Metadata)
			tr.RawResult = rawResult
			if startedAt.Valid {
				tr.StartedAt = startedAt.Time
			}
			if endedAt.Valid {
				tr.EndedAt = endedAt.Time
			}
			out = append(out, tr)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("tool_trace_store: list: %w", err)
	}
	return out, nil
}

func marshalJSONText(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalJSONTextLimit(v any, maxBytes int) (string, bool, error) {
	out, err := marshalJSONText(v)
	if err != nil {
		return "", false, err
	}
	truncated, didTruncate := truncateString(out, maxBytes)
	if !didTruncate {
		return truncated, false, nil
	}
	fallback, err := marshalJSONText(map[string]any{
		"truncated": true,
		"preview":   truncated,
	})
	if err != nil {
		return "", false, err
	}
	return fallback, true, nil
}

func redactJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if isSensitiveKey(k) {
				out[k] = redactedValue
				continue
			}
			out[k] = redactJSONValue(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, redactJSONValue(val))
		}
		return out
	default:
		return x
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "password", "token", "api_key", "apikey", "authorization", "secret":
		return true
	default:
		return strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "token") ||
			strings.Contains(normalized, "api_key") ||
			strings.Contains(normalized, "apikey") ||
			strings.Contains(normalized, "authorization") ||
			strings.Contains(normalized, "secret")
	}
}

func redactSensitiveText(s string) string {
	if s == "" {
		return ""
	}
	return sensitiveTextPattern.ReplaceAllStringFunc(s, func(match string) string {
		if idx := strings.Index(match, "="); idx >= 0 {
			return match[:idx+1] + redactedValue
		}
		return redactedValue
	})
}

func truncateString(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	out := s[:maxBytes]
	for !utf8.ValidString(out) && len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out, true
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
