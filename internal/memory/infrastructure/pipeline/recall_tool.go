package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
	jschema "github.com/byteBuilderX/stratum/pkg/jsonschema"
	"github.com/byteBuilderX/stratum/pkg/observability"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
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
		"input_schema": jschema.Must(jschema.Object(
			jschema.RequiredProp("query", jschema.String("Search query to find relevant memories")),
			jschema.OptionalProp("limit", jschema.Integer(nil, nil, "Max results (1-20, default 5)")),
		)).Map(),
	}
}

// vectorSearcher is the minimal slice of *vector.VectorStore that recall needs.
// Narrowing to this interface (rather than the concrete store) lets the
// dual-collection fusion in tryVectorSearch be unit-tested with a fake, without
// standing up Milvus. *vector.VectorStore satisfies it via SearchWithFilter.
type vectorSearcher interface {
	SearchWithFilter(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string, partitions ...string) ([]vector.SearchResult, error)
}

// recallDB is the minimal slice of *pgxpool.Pool that text recall needs.
// Narrowing to this interface (rather than the concrete pool) lets Handle's
// text fallback be unit-tested with pgxmock, mirroring the vectorSearcher
// narrowing above and the tenantPool pattern in persistence.
type recallDB interface {
	Begin(context.Context) (pgx.Tx, error)
}

// RecallHandler executes recall_memory queries against the memory_entries table.
// It retrieves semantic and text candidates, then fuses them with RRF.
type RecallHandler struct {
	pool          recallDB
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

	vectorCandidates, vecErr := h.tryVectorSearch(ctx, tenantID, userID, agentID, scope, req)
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
		zap.Int("results", len(results)),
		// vecErr 非 nil 表示向量库 outage，已在 searchAllCollections 内 ERROR log
		// + degraded 指标；此处仅随 Debug 链路追溯，zap.Error(nil) 自动省略。
		zap.Error(vecErr))
	h.metrics.IncKnowledgeQuery("recall", "success")
	return string(out), nil
}

// tryVectorSearch 返回向量候选与是否发生向量库 outage。非 nil 的 error 表示
// 至少一个候选 collection 遭遇非 not-found 的查询失败（见 searchAllCollections
// 的分类）——调用方（Handle）保持 text 降级契约不变，outage 信号由
// searchAllCollections 内以 ERROR log + degraded 指标发出。
func (h *RecallHandler) tryVectorSearch(ctx context.Context, tenantID, userID, agentID, scope string, req RecallRequest) ([]recallCandidate, error) {
	embedSvc := h.embedSvc
	if embedSvc == nil && h.embedResolver != nil {
		embedSvc = h.embedResolver(ctx, tenantID)
	}
	if embedSvc == nil || h.vectorDB == nil {
		return nil, nil
	}

	vec, err := embedSvc.EmbedVector(ctx, req.Query)
	if err != nil {
		h.logger.Debug("memory.recall: embed failed, falling back to text search", zap.Error(err))
		return nil, nil
	}

	if strings.ContainsAny(userID, `"'\`) {
		return nil, nil
	}
	var expr string
	if scope == "agent" && agentID != "" && !strings.ContainsAny(agentID, `"'\`) {
		expr = fmt.Sprintf(`user_id == "%s" && agent_id == "%s" && scope == "agent"`, userID, agentID)
	} else if userID != "" {
		expr = fmt.Sprintf(`user_id == "%s" && scope == "user"`, userID)
	}

	// 候选集合 = 当前模型的新名 collection（raw + facts）∪ legacy 名（升级前
	// 数据）。SearchWithFilter 对不存在的 collection 报错被跳过，dim mismatch
	// （模型切换后旧集合维度不符）同样跳过——天然实现 legacy 回退（不
	// fail-closed），详见 searchAllCollections 的错误分类。
	// embedSvc 已由上方 guard 保证非 nil；Model() 可能为空串 → legacy-only。
	merged, searchErr := h.searchAllCollections(ctx, recallCandidateCollections(tenantID, embedSvc.Model()), vec, req.Limit*2, expr)

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
	return entries, searchErr
}

// recallCandidateCollections 返回查询候选：模型非空 → [新 raw, legacy raw,
// 新 facts, legacy facts]（升级后数据在新名，升级前在 legacy 名）；模型未知
// → 仅 legacy 对（空模型后缀的新名无意义且升级前数据都在旧名）。
func recallCandidateCollections(tenantID, embedModel string) []string {
	if embedModel == "" {
		return []string{memoryCollectionLegacyName(tenantID), memoryFactsCollectionLegacyName(tenantID)}
	}
	return []string{
		memoryCollectionName(tenantID, embedModel), memoryCollectionLegacyName(tenantID),
		memoryFactsCollectionName(tenantID, embedModel), memoryFactsCollectionLegacyName(tenantID),
	}
}

// searchAllCollections 对每个候选 collection 查询并合并结果；单个 collection
// 查询失败不 fail-closed——legacy 回退由"先新名后旧名"的顺序天然实现。失败按
// 性质分类：collection-not-found 与 dim mismatch（模型切换后旧集合维度不符）
// 都是升级前存量数据的预期状态，Debug 后静默跳过；其余错误（Milvus 连接/
// 超时等）才是向量库 outage——降级保留，但必须 ERROR 可见并计入 degraded
// 指标，禁止无声降级。返回的 error 为首个 outage（nil 表示无 outage），供调用
// 方追溯。
func (h *RecallHandler) searchAllCollections(ctx context.Context, collections []string, vec []float32, limit int, expr string) ([]vector.SearchResult, error) {
	var merged []vector.SearchResult
	var outageErr error
	for _, collection := range collections {
		results, err := h.vectorDB.SearchWithFilter(ctx, collection, vec, limit, expr)
		if err == nil {
			merged = append(merged, results...)
			continue
		}
		if errors.Is(err, storagemilvus.ErrCollectionNotFound) {
			h.logger.Debug("memory.recall: collection not found, legacy fallback",
				zap.String("collection", collection))
			continue
		}
		if errors.Is(err, storagemilvus.ErrDimensionMismatch) {
			// 模型切换后旧集合维度与当前 embedding 不一致：确定性数据形态错误，
			// 与 collection-not-found 同级——Debug 跳过，不 ERROR、不计 degraded、
			// 不构成 outage。
			h.logger.Debug("memory.recall: collection dimension mismatch, legacy fallback",
				zap.String("collection", collection))
			continue
		}
		if outageErr == nil {
			outageErr = err
			h.metrics.IncKnowledgeQuery("recall", "degraded")
		}
		h.logger.Error("memory.recall.vector_search_failed",
			zap.String("collection", collection), zap.Error(err))
	}
	return merged, outageErr
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
