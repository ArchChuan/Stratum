package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// RecallRequest holds the parsed input for the recall_memory tool.
type RecallRequest struct {
	Query string `json:"query"`
	Scope string `json:"scope"`
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
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"private", "personal", "shared"},
					"description": "private=this user+agent, personal=this user across agents, shared=all tenant memories",
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
type RecallHandler struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewRecallHandler creates a RecallHandler backed by the given pool.
func NewRecallHandler(pool *pgxpool.Pool, logger *zap.Logger) *RecallHandler {
	return &RecallHandler{pool: pool, logger: logger}
}

// Handle executes the recall_memory tool invocation.
func (h *RecallHandler) Handle(ctx context.Context, tenantID, userID, agentID string, input map[string]any) (string, error) {
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
	if req.Scope == "" {
		req.Scope = "private"
	}

	schema := "tenant_" + tenantID
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return "", fmt.Errorf("set schema: %w", err)
	}

	// Build query with parameterized filters
	baseQuery := `SELECT content, role, importance, created_at FROM memory_entries WHERE enriched_at IS NOT NULL`
	args := []any{}
	argIdx := 1

	// Text search filter using the query
	baseQuery += fmt.Sprintf(" AND content ILIKE '%%' || $%d || '%%'", argIdx)
	args = append(args, req.Query)
	argIdx++

	switch req.Scope {
	case "private":
		baseQuery += fmt.Sprintf(" AND user_id = $%d AND agent_id = $%d", argIdx, argIdx+1)
		args = append(args, userID, agentID)
		argIdx += 2
	case "personal":
		baseQuery += fmt.Sprintf(" AND user_id = $%d", argIdx)
		args = append(args, userID)
		argIdx++
	case "shared":
		// shared still scopes to the requesting user — avoids cross-user leakage within tenant
		baseQuery += fmt.Sprintf(" AND user_id = $%d", argIdx)
		args = append(args, userID)
		argIdx++
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
	h.logger.Debug("memory.recall",
		zap.String("tenant_id", tenantID),
		zap.String("query", req.Query),
		zap.Int("results", len(results)))
	return string(out), nil
}
