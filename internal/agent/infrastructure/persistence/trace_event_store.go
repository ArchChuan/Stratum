package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/jackc/pgx/v5"
)

type PgTraceEventStore struct {
	pool chatPoolIface
}

func NewPgTraceEventStore(pool chatPoolIface) *PgTraceEventStore {
	return &PgTraceEventStore{pool: pool}
}

func (s *PgTraceEventStore) Insert(ctx context.Context, tenantID string, event domain.AgentTraceEvent) error {
	return s.InsertBatch(ctx, tenantID, []domain.AgentTraceEvent{event})
}

func (s *PgTraceEventStore) InsertBatch(ctx context.Context, tenantID string, events []domain.AgentTraceEvent) error {
	if len(events) == 0 {
		return nil
	}
	return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		for _, ev := range events {
			inputJSON, err := marshalJSONText(ev.Input)
			if err != nil {
				return fmt.Errorf("trace_event_store: marshal input: %w", err)
			}
			outputJSON, err := marshalJSONText(ev.Output)
			if err != nil {
				return fmt.Errorf("trace_event_store: marshal output: %w", err)
			}
			metadataJSON, err := marshalJSONText(ev.Metadata)
			if err != nil {
				return fmt.Errorf("trace_event_store: marshal metadata: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO agent_trace_events
				 (trace_id, execution_id, conversation_id, agent_id, user_id,
				  run_type, observation_type, event_type, step_index, span_name, parent_event_id,
				  status, input_json, output_json,
				  summary, error_message, model, prompt_tokens, completion_tokens, total_tokens,
				  cost_usd, latency_ms, tool_trace_id, provider_type, provider_id, node_id, node_type,
				  workflow_id, workflow_version, sequence_no, metadata_json, otel_trace_id, otel_span_id,
				  started_at, ended_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35)`,
				ev.TraceID, ev.ExecutionID, ev.ConversationID, ev.AgentID, ev.UserID,
				ev.RunType, ev.ObservationType, ev.EventType, ev.StepIndex, ev.SpanName, ev.ParentEventID,
				ev.Status, inputJSON, outputJSON,
				ev.Summary, ev.ErrorMessage, ev.Model, ev.PromptTokens, ev.CompletionTokens, ev.TotalTokens,
				ev.CostUSD, ev.LatencyMs, ev.ToolTraceID, ev.ProviderType, ev.ProviderID, ev.NodeID, ev.NodeType,
				ev.WorkflowID, ev.WorkflowVersion, ev.SequenceNo, metadataJSON, ev.OTelTraceID, ev.OTelSpanID,
				nullableTime(ev.StartedAt), nullableTime(ev.EndedAt),
			); err != nil {
				return fmt.Errorf("trace_event_store: insert: %w", err)
			}
		}
		return nil
	})
}

func (s *PgTraceEventStore) ListByTraceID(ctx context.Context, tenantID, traceID string) ([]domain.AgentTraceEvent, error) {
	var out []domain.AgentTraceEvent
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, trace_id, execution_id, conversation_id, agent_id, user_id,
			        run_type, observation_type, event_type, step_index, span_name, parent_event_id, status,
			        input_json, output_json, summary, error_message, model,
			        prompt_tokens, completion_tokens, total_tokens, cost_usd, latency_ms,
			        tool_trace_id, provider_type, provider_id, node_id, node_type,
			        workflow_id, workflow_version, sequence_no, metadata_json, otel_trace_id,
			        otel_span_id, started_at, ended_at, created_at
			   FROM agent_trace_events
			  WHERE trace_id = $1
			  ORDER BY created_at ASC`,
			traceID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ev domain.AgentTraceEvent
			var inputRaw, outputRaw, metadataRaw json.RawMessage
			var startedAt, endedAt sql.NullTime
			if err := rows.Scan(
				&ev.ID, &ev.TraceID, &ev.ExecutionID, &ev.ConversationID, &ev.AgentID, &ev.UserID,
				&ev.RunType, &ev.ObservationType, &ev.EventType, &ev.StepIndex, &ev.SpanName, &ev.ParentEventID, &ev.Status,
				&inputRaw, &outputRaw, &ev.Summary, &ev.ErrorMessage, &ev.Model,
				&ev.PromptTokens, &ev.CompletionTokens, &ev.TotalTokens, &ev.CostUSD, &ev.LatencyMs,
				&ev.ToolTraceID, &ev.ProviderType, &ev.ProviderID, &ev.NodeID, &ev.NodeType,
				&ev.WorkflowID, &ev.WorkflowVersion, &ev.SequenceNo, &metadataRaw, &ev.OTelTraceID,
				&ev.OTelSpanID, &startedAt, &endedAt, &ev.CreatedAt,
			); err != nil {
				return err
			}
			ev.Input = inputRaw
			ev.Output = outputRaw
			ev.Metadata = metadataRaw
			if startedAt.Valid {
				ev.StartedAt = startedAt.Time
			}
			if endedAt.Valid {
				ev.EndedAt = endedAt.Time
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("trace_event_store: list: %w", err)
	}
	return out, nil
}
