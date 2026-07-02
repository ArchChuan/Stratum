package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	vector "github.com/byteBuilderX/stratum/pkg/vector"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// RecallRequest holds the parsed input for the recall_memory tool.
type RecallRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// RecallEntry represents a single memory result returned to the agent.
type RecallEntry struct {
	Content    string  `json:"content"`
	Role       string  `json:"role"`
	Importance float64 `json:"importance"`
	CreatedAt  string  `json:"created_at"`
}

// RecallResult is a slice of recalled memory entries.
type RecallResult []RecallEntry

// RecallToolDefinition returns the tool schema for recall_memory.
func RecallToolDefinition() map[string]any {
	return map[string]any{
		"name":        "stratum_recall_memory",
		"description": "Search long-term memory for relevant past interactions, entities, and context. Use when you need to recall information from previous conversations.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query to find relevant memories",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (1-20, default 5)",
				},
			},
			"required": []string{"query"},
		},
	}
}

// RecallHandler executes recall_memory queries against the memory_entries table.
// When embedResolver and vectorDB are available, it uses vector search for semantic recall;
// otherwise falls back to ILIKE text search.
type RecallHandler struct {
	pool          *pgxpool.Pool
	logger        *zap.Logger
	embedSvc      EmbedClient
	embedResolver EmbedServiceResolver
	vectorDB      *vector.VectorStore
	metrics       observability.MetricsProvider
}

// NewRecallHandler creates a RecallHandler backed by the given pool.
func NewRecallHandler(pool *pgxpool.Pool, logger *zap.Logger, embedSvc EmbedClient, embedResolver EmbedServiceResolver, vectorDB *vector.VectorStore) *RecallHandler {
	return &RecallHandler{pool: pool, logger: logger, embedSvc: embedSvc, embedResolver: embedResolver, vectorDB: vectorDB, metrics: observability.NoopMetrics{}}
}

// WithMetrics injects a MetricsProvider; returns the handler for chaining.
func (h *RecallHandler) WithMetrics(m observability.MetricsProvider) *RecallHandler {
	h.metrics = m
	return h
}

// Handle executes the recall_memory tool invocation.
func (h *RecallHandler) Handle(ctx context.Context, tenantID, userID, agentID, scope string, input map[string]any) (string, error) {
	start := time.Now()
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}
	var req RecallRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", fmt.Errorf("unmarshal request: %w", err)
	}

	if req.Query == "" {
		return "error: query is required", nil
	}
	if req.Limit <= 0 || req.Limit > 20 {
		req.Limit = 5
	}

	// Try vector search first (semantic recall)
	if results := h.tryVectorSearch(ctx, tenantID, userID, agentID, scope, req); len(results) > 0 {
		sc, _ := observability.SpanFromContext(ctx)
		out, _ := json.Marshal(results)
		h.logger.Debug("memory.recall.vector",
			zap.String("trace_id", sc.TraceID),
			zap.String("tenant_id", tenantID),
			zap.String("query", req.Query),
			zap.Int("results", len(results)))
		h.metrics.RecordMemoryRetrievalDuration("recall_vector", time.Since(start).Seconds())
		h.metrics.IncKnowledgeQuery("recall", "success")
		return string(out), nil
	}

	// Fallback: ILIKE text search
	result, err := h.textSearch(ctx, tenantID, userID, agentID, scope, req)
	h.metrics.RecordMemoryRetrievalDuration("recall_text", time.Since(start).Seconds())
	if err != nil {
		h.metrics.IncKnowledgeQuery("recall", "error")
	} else {
		h.metrics.IncKnowledgeQuery("recall", "success")
	}
	return result, err
}

func (h *RecallHandler) tryVectorSearch(ctx context.Context, tenantID, userID, agentID, scope string, req RecallRequest) RecallResult {
	embedSvc := h.embedSvc
	if embedSvc == nil && h.embedResolver != nil {
		embedSvc = h.embedResolver(ctx, tenantID)
	}
	if embedSvc == nil || h.vectorDB == nil {
		return nil
	}

	vec, err := embedSvc.EmbedVector(ctx, req.Query)
	if err != nil {
		h.logger.Debug("memory.recall: embed failed, falling back to text search", zap.Error(err))
		return nil
	}

	collection := memoryCollectionName(tenantID)
	if strings.ContainsAny(userID, `"'\`) {
		return nil
	}
	var expr string
	if scope == "agent" && agentID != "" && !strings.ContainsAny(agentID, `"'\`) {
		expr = fmt.Sprintf(`user_id == "%s" && agent_id == "%s" && scope == "agent"`, userID, agentID)
	} else if userID != "" {
		expr = fmt.Sprintf(`user_id == "%s" && scope == "user"`, userID)
	}

	results, err := h.vectorDB.SearchWithFilter(ctx, collection, vec, constants.MemoryLongTermTopK, expr)
	if err != nil {
		h.logger.Debug("memory.recall: vector search failed, falling back", zap.Error(err))
		return nil
	}

	var entries RecallResult
	for _, r := range results {
		if r.Content != "" {
			entries = append(entries, RecallEntry{
				Content: r.Content,
			})
		}
	}
	return entries
}

func (h *RecallHandler) textSearch(ctx context.Context, tenantID, userID, agentID, scope string, req RecallRequest) (string, error) {
	schema := "tenant_" + tenantID
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return "", fmt.Errorf("set schema: %w", err)
	}

	baseQuery := `SELECT content, role, importance, created_at FROM memory_entries WHERE enriched_at IS NOT NULL`
	args := []any{}
	argIdx := 1

	// Text search filter
	baseQuery += fmt.Sprintf(" AND content ILIKE '%%' || $%d || '%%'", argIdx)
	args = append(args, req.Query)
	argIdx++

	baseQuery += fmt.Sprintf(" AND user_id = $%d", argIdx)
	args = append(args, userID)
	argIdx++

	if scope == "agent" && agentID != "" {
		baseQuery += fmt.Sprintf(" AND agent_id = $%d AND scope = 'agent'", argIdx)
		args = append(args, agentID)
		argIdx++
	} else {
		baseQuery += " AND scope = 'user'"
	}

	baseQuery += " ORDER BY importance DESC, created_at DESC"
	baseQuery += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, req.Limit)

	rows, err := tx.Query(ctx, baseQuery, args...)
	if err != nil {
		return "", fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var results RecallResult
	for rows.Next() {
		var e RecallEntry
		var createdAt any
		if err := rows.Scan(&e.Content, &e.Role, &e.Importance, &createdAt); err != nil {
			continue
		}
		e.CreatedAt = fmt.Sprintf("%v", createdAt)
		results = append(results, e)
	}

	if len(results) == 0 {
		return "No relevant memories found.", nil
	}

	out, _ := json.Marshal(results)
	sc, _ := observability.SpanFromContext(ctx)
	h.logger.Debug("memory.recall",
		zap.String("trace_id", sc.TraceID),
		zap.String("tenant_id", tenantID),
		zap.String("query", req.Query),
		zap.Int("results", len(results)))
	return string(out), nil
}
