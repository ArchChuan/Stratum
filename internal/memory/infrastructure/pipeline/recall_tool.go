package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

type recallCandidate struct {
	ID    string
	Entry RecallEntry
}

type scoredRecallCandidate struct {
	candidate recallCandidate
	score     float64
	textHit   bool
}

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

// vectorSearcher is the minimal slice of *vector.VectorStore that recall needs.
// Narrowing to this interface (rather than the concrete store) lets the
// dual-collection fusion in tryVectorSearch be unit-tested with a fake, without
// standing up Milvus. *vector.VectorStore satisfies it via SearchWithFilter.
type vectorSearcher interface {
	SearchWithFilter(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string, partitions ...string) ([]vector.SearchResult, error)
}

// RecallHandler executes recall_memory queries against the memory_entries table.
// It retrieves semantic and text candidates, then fuses them with RRF.
type RecallHandler struct {
	pool          *pgxpool.Pool
	logger        *zap.Logger
	embedSvc      EmbedClient
	embedResolver EmbedServiceResolver
	vectorDB      vectorSearcher
	metrics       observability.MetricsProvider
}

// NewRecallHandler creates a RecallHandler backed by the given pool.
func NewRecallHandler(pool *pgxpool.Pool, logger *zap.Logger, embedSvc EmbedClient, embedResolver EmbedServiceResolver, vectorDB *vector.VectorStore) *RecallHandler {
	h := &RecallHandler{pool: pool, logger: logger, embedSvc: embedSvc, embedResolver: embedResolver, metrics: observability.NoopMetrics{}}
	// Guard against the typed-nil trap: a nil *vector.VectorStore stored in an
	// interface field is NOT == nil, so tryVectorSearch's nil check would pass
	// and then panic. Only assign when the concrete pointer is non-nil.
	if vectorDB != nil {
		h.vectorDB = vectorDB
	}
	return h
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

	vectorCandidates := h.tryVectorSearch(ctx, tenantID, userID, agentID, scope, req)
	textCandidates, err := h.textSearchCandidates(ctx, tenantID, userID, agentID, scope, req)
	h.metrics.RecordMemoryRetrievalDuration("recall_hybrid", time.Since(start).Seconds())
	if err != nil && len(vectorCandidates) == 0 {
		h.metrics.IncKnowledgeQuery("recall", "error")
		return "", err
	}
	if err != nil {
		h.logger.Debug("memory.recall: text search failed, using vector candidates", zap.Error(err))
	}

	results := fuseRecallCandidates(vectorCandidates, textCandidates, req.Limit)
	if len(results) == 0 {
		h.metrics.IncKnowledgeQuery("recall", "success")
		return "No relevant memories found.", nil
	}

	out, _ := json.Marshal(results)
	sc, _ := observability.SpanFromContext(ctx)
	h.logger.Debug("memory.recall.hybrid",
		zap.String("trace_id", sc.TraceID),
		zap.String("tenant_id", tenantID),
		zap.String("query", req.Query),
		zap.Int("vector_results", len(vectorCandidates)),
		zap.Int("text_results", len(textCandidates)),
		zap.Int("results", len(results)))
	h.metrics.IncKnowledgeQuery("recall", "success")
	return string(out), nil
}

func (h *RecallHandler) tryVectorSearch(ctx context.Context, tenantID, userID, agentID, scope string, req RecallRequest) []recallCandidate {
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

	if strings.ContainsAny(userID, `"'\`) {
		return nil
	}
	var expr string
	if scope == "agent" && agentID != "" && !strings.ContainsAny(agentID, `"'\`) {
		expr = fmt.Sprintf(`user_id == "%s" && agent_id == "%s" && scope == "agent"`, userID, agentID)
	} else if userID != "" {
		expr = fmt.Sprintf(`user_id == "%s" && scope == "user"`, userID)
	}

	// Query both the raw-turn collection (Pipeline A: enrich→embed) and the
	// extracted-facts collection (Pipeline B: extract→embed). They share the
	// same Milvus schema and scope fields; fusing them here is the only place
	// distilled fact vectors become reachable by semantic recall.
	var merged []vector.SearchResult
	for _, collection := range []string{memoryCollectionName(tenantID), memoryFactsCollectionName(tenantID)} {
		results, err := h.vectorDB.SearchWithFilter(ctx, collection, vec, req.Limit*2, expr)
		if err != nil {
			h.logger.Debug("memory.recall: vector search failed for collection, skipping",
				zap.String("collection", collection), zap.Error(err))
			continue
		}
		merged = append(merged, results...)
	}

	// Sort by ascending L2 distance (smaller = more similar) so downstream RRF
	// ranks the closest match across both collections first.
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score < merged[j].Score })

	var entries []recallCandidate
	for _, r := range merged {
		if r.Content != "" {
			entries = append(entries, recallCandidate{
				ID: r.ID,
				Entry: RecallEntry{
					Content: r.Content,
				},
			})
		}
	}
	return entries
}

func (h *RecallHandler) textSearchCandidates(ctx context.Context, tenantID, userID, agentID, scope string, req RecallRequest) ([]recallCandidate, error) {
	if h.pool == nil {
		return nil, nil
	}
	schema := "tenant_" + tenantID
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return nil, fmt.Errorf("set schema: %w", err)
	}

	baseQuery := `SELECT id, content, role, importance, created_at FROM memory_entries WHERE enriched_at IS NOT NULL`
	args := []any{}
	argIdx := 1

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
	args = append(args, req.Limit*2)

	rows, err := tx.Query(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	var results []recallCandidate
	for rows.Next() {
		var id string
		var e RecallEntry
		var createdAt any
		if err := rows.Scan(&id, &e.Content, &e.Role, &e.Importance, &createdAt); err != nil {
			continue
		}
		e.CreatedAt = fmt.Sprintf("%v", createdAt)
		results = append(results, recallCandidate{ID: id, Entry: e})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan memories: %w", err)
	}
	return results, nil
}

func fuseRecallCandidates(vectorCandidates, textCandidates []recallCandidate, topK int) RecallResult {
	if topK <= 0 {
		topK = 5
	}
	byID := make(map[string]scoredRecallCandidate, len(vectorCandidates)+len(textCandidates))
	k := float64(constants.MemoryRRFConstant)

	for rank, candidate := range vectorCandidates {
		if candidate.ID == "" {
			candidate.ID = candidate.Entry.Content
		}
		current := byID[candidate.ID]
		if current.candidate.ID == "" {
			current.candidate = candidate
		}
		current.score += 1.0 / (k + float64(rank+1))
		byID[candidate.ID] = current
	}

	for rank, candidate := range textCandidates {
		if candidate.ID == "" {
			candidate.ID = candidate.Entry.Content
		}
		current := byID[candidate.ID]
		if current.candidate.ID == "" || current.candidate.Entry.Role == "" {
			current.candidate = candidate
		}
		current.score += 1.0 / (k + float64(rank+1))
		current.textHit = true
		byID[candidate.ID] = current
	}

	scored := make([]scoredRecallCandidate, 0, len(byID))
	for _, candidate := range byID {
		scored = append(scored, candidate)
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].textHit != scored[j].textHit {
			return scored[i].textHit
		}
		return scored[i].candidate.Entry.Importance > scored[j].candidate.Entry.Importance
	})

	if topK > len(scored) {
		topK = len(scored)
	}
	out := make(RecallResult, 0, topK)
	for i := 0; i < topK; i++ {
		out = append(out, scored[i].candidate.Entry)
	}
	return out
}
