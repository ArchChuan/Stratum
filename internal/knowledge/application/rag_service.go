// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

var ErrRAGDependency = errors.New("knowledge retrieval dependency unavailable")

// hybridLegRecallFactor widens TopK for the hybrid retrieval legs: each leg
// (vector, keyword) recalls TopK × hybridLegRecallFactor candidates before
// reciprocal rank fusion narrows the pool back to TopK. External reranking
// widens further via RerankWidenFactor.
const hybridLegRecallFactor = 2

// errCollectionNotFound distinguishes a missing Milvus collection from other
// search failures so Query can decide between "legitimately empty workspace"
// and "drift" via ChunkRepo.CountByWorkspace.
var errCollectionNotFound = errors.New("knowledge collection not found")

func isCollectionNotFound(err error) bool {
	return errors.Is(err, errCollectionNotFound) ||
		strings.Contains(err.Error(), "collection not found")
}

// rerankIdentity splits "provider:model" style rerank identities.
// provider "builtin-score-v1" is local reordering; any other provider
// requires an external reranker backend.
func rerankIdentity(identity string) (provider, model string) {
	if i := strings.Index(identity, ":"); i >= 0 {
		return identity[:i], identity[i+1:]
	}
	return identity, ""
}

// NewRAGSearchFn returns a knowledge search function suitable for the agent's
// WithRAGSearchFn hook. It fans out across workspaces concurrently, bounded
// by MaxConcurrentWorkspaceSearch (a per-query cap protecting the embed
// backend and DB pool), and concatenates results; the first error is returned
// only when no content was produced (at-least-one semantics).
func NewRAGSearchFn(rs *RAGService, tenantID string) func(
	ctx context.Context, workspaces []string, query string, topK int,
) (string, error) {
	return func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
		results := make([]wsResult, len(workspaces))
		sem := make(chan struct{}, constants.MaxConcurrentWorkspaceSearch)
		var wg sync.WaitGroup
		for i, ws := range workspaces {
			// Acquire before spawning so at most MaxConcurrentWorkspaceSearch
			// searches are in flight; the launch loop parks here otherwise.
			sem <- struct{}{}
			wg.Add(1)
			go func(i int, ws string) {
				defer wg.Done()
				defer func() { <-sem }()
				results[i] = searchWorkspace(ctx, rs, tenantID, ws, query, topK)
			}(i, ws)
		}
		wg.Wait()
		return rs.mergeResults(results)
	}
}

func searchWorkspace(ctx context.Context, rs *RAGService, tenantID, ws, query string, topK int) wsResult {
	mode, effectiveTopK, embedModel, workspaceID, err := resolveWorkspaceConfig(ctx, rs, tenantID, ws, topK)
	if err != nil {
		return wsResult{err: err}
	}
	out, err := rs.Query(ctx, RAGQueryRequest{
		WorkspaceID:    workspaceID,
		Workspace:      ws,
		Question:       query,
		TenantID:       tenantID,
		Mode:           mode,
		TopK:           effectiveTopK,
		EmbeddingModel: embedModel,
	})
	if err != nil {
		return wsResult{err: err}
	}
	return wsResult{content: formatSources(out.Sources)}
}

func resolveWorkspaceConfig(ctx context.Context, rs *RAGService, tenantID, ws string, topK int) (
	mode string, effectiveTopK int, embedModel string, workspaceID string, err error,
) {
	mode = domain.DefaultQueryMode
	effectiveTopK = topK
	if rs.wsRepo == nil {
		return
	}
	w, getErr := rs.wsRepo.GetByName(ctx, tenantID, ws)
	if getErr != nil {
		err = ErrRAGDependency
		return
	}
	if w == nil {
		return
	}
	workspaceID = w.ID
	if w.Config.TopK > 0 {
		effectiveTopK = w.Config.TopK
	}
	embedModel = w.Config.EmbeddingModel
	if w.Config.QueryMode != "" {
		mode = w.Config.QueryMode
	}
	return
}

func formatSources(sources []Source) string {
	var sb strings.Builder
	for _, src := range sources {
		sb.WriteString(src.Content)
		sb.WriteString("\n---\n")
	}
	return sb.String()
}

type wsResult struct {
	content string
	err     error
}

// mergeResults concatenates per-workspace content with at-least-one
// semantics: failed workspaces are skipped, successful ones contribute, and
// the first error surfaces only when nothing was produced at all. Partial
// failure is deliberately not fatal (one dead workspace must not blank the
// whole answer) but it must not be silent either — a WARN with the failure
// count and first error is emitted so operators can see the degraded fan-out.
func (rs *RAGService) mergeResults(results []wsResult) (string, error) {
	var combined strings.Builder
	var firstErr error
	failures := 0
	for _, r := range results {
		if r.err != nil {
			failures++
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		combined.WriteString(r.content)
	}
	if failures > 0 {
		rs.logger.Warn("knowledge.rag.partial_failure",
			zap.Int("failed_workspaces", failures),
			zap.Int("total_workspaces", len(results)),
			zap.Error(firstErr))
	}
	if combined.Len() == 0 && firstErr != nil {
		return "", firstErr
	}
	return combined.String(), nil
}

type RAGService struct {
	embeddingSvc  knowledgeport.Embedder
	embedResolver EmbedResolver
	wsRepo        knowledgeport.WorkspaceRepo
	chunkRepo     knowledgeport.ChunkRepo
	vectorStore   knowledgeport.VectorStore
	reranker      knowledgeport.Reranker
	metrics       observability.MetricsProvider
	logger        *zap.Logger
}

func NewRAGService(
	embeddingSvc knowledgeport.Embedder,
	vectorStore knowledgeport.VectorStore,
	logger *zap.Logger,
) *RAGService {
	return &RAGService{
		embeddingSvc: embeddingSvc,
		vectorStore:  vectorStore,
		logger:       logger,
	}
}

func (rs *RAGService) SetEmbedResolver(r EmbedResolver)                  { rs.embedResolver = r }
func (rs *RAGService) SetWorkspaceRepo(repo knowledgeport.WorkspaceRepo) { rs.wsRepo = repo }
func (rs *RAGService) SetChunkRepo(repo knowledgeport.ChunkRepo)         { rs.chunkRepo = repo }
func (rs *RAGService) SetReranker(r knowledgeport.Reranker)              { rs.reranker = r }
func (rs *RAGService) SetMetrics(m observability.MetricsProvider)        { rs.metrics = m }

func (rs *RAGService) resolveEmbedder(ctx context.Context, req RAGQueryRequest) knowledgeport.Embedder {
	if rs.embedResolver != nil && req.TenantID != "" {
		if c := rs.embedResolver(ctx, req.TenantID, req.EmbeddingModel); c != nil {
			return c
		}
	}
	return rs.embeddingSvc
}

type RAGQueryRequest struct {
	Question       string
	Workspace      string
	WorkspaceID    string // stable ID for collection naming; resolved from Workspace if empty
	TenantID       string
	Mode           string // "vector", "keyword", "graph", "hybrid"
	TopK           int
	EmbeddingModel string
	// Reranking selects the rerank strategy: "" (none), "builtin-score-v1"
	// (local score desc), or "cohere:<model>" (external reranker).
	Reranking      string
	ScoreThreshold float32 // keep only results with Score >= threshold; 0 disables (keyword mode exempt)
	RerankTopK     int     // final count after external reranking; 0 uses TopK
}

type RAGQueryResult struct {
	Answer        string
	Sources       []Source
	VectorResults []knowledgeport.VectorSearchResult
	Mode          string
	Latency       time.Duration
}

type Source struct {
	DocumentID    string
	ChunkID       string
	Content       string
	ParentContent string // non-empty when parent chunk was fetched (Parent-Child strategy)
	ChunkIndex    int64
	Score         float32
}

func (rs *RAGService) Query(ctx context.Context, req RAGQueryRequest) (*RAGQueryResult, error) {
	startTime := time.Now()
	sc, _ := observability.SpanFromContext(ctx)
	rs.logger.Info("executing RAG query",
		zap.String("trace_id", sc.TraceID),
		zap.Int("question_length", len([]rune(req.Question))),
		zap.String("mode", req.Mode))

	result := &RAGQueryResult{
		Mode:    req.Mode,
		Answer:  "",
		Sources: []Source{},
		Latency: 0,
	}

	if req.TopK <= 0 {
		req.TopK = constants.DefaultRAGTopK
	}

	if req.WorkspaceID == "" && req.Workspace != "" && rs.wsRepo != nil {
		ws, err := rs.wsRepo.GetByName(ctx, req.TenantID, req.Workspace)
		if err != nil {
			return nil, ErrRAGDependency
		}
		req.WorkspaceID = ws.ID
		if req.EmbeddingModel == "" {
			req.EmbeddingModel = ws.Config.EmbeddingModel
		}
	}

	collectionName := constants.CollectionName(req.TenantID, req.WorkspaceID)

	switch req.Mode {
	case "vector":
		candidateTopK := req.TopK
		if widensRecall(req.Reranking) {
			candidateTopK = req.TopK * constants.RerankWidenFactor
		}
		vectorResults, err := rs.queryVector(ctx, req.Question, collectionName, candidateTopK, rs.resolveEmbedder(ctx, req), req.EmbeddingModel)
		if err != nil {
			if errors.Is(err, errCollectionNotFound) {
				if missingErr := rs.handleMissingCollection(ctx, req); missingErr != nil {
					return nil, missingErr
				}
				return result, nil
			}
			rs.logger.Error("knowledge.retrieval.dependency_failed", zap.String("trace_id", sc.TraceID),
				zap.String("operation", "vector_search"), zap.String("error_category", "dependency_unavailable"))
			return nil, ErrRAGDependency
		}
		result.VectorResults = vectorResults

		sources, rerankErr := rs.rerankSources(ctx, req, vectorToPool(vectorResults))
		if rerankErr != nil {
			return nil, rerankErr
		}
		result.Sources = sources

	case "keyword":
		if rs.chunkRepo == nil {
			return nil, fmt.Errorf("keyword search not available: chunk store not configured")
		}
		if req.WorkspaceID == "" {
			return nil, fmt.Errorf("keyword search requires workspace ID")
		}
		chunks, err := rs.chunkRepo.KeywordSearch(ctx, req.TenantID, req.WorkspaceID, req.Question, req.TopK)
		if err != nil {
			rs.logger.Error("knowledge.retrieval.dependency_failed", zap.String("trace_id", sc.TraceID),
				zap.String("operation", "keyword_search"), zap.String("error_category", "dependency_unavailable"))
			return nil, ErrRAGDependency
		}
		for _, c := range chunks {
			result.Sources = append(result.Sources, Source{
				DocumentID: c.DocID,
				ChunkID:    c.ID,
				Content:    c.Text,
				ChunkIndex: c.Index,
			})
		}

	case "hybrid":
		embedder := rs.resolveEmbedder(ctx, req)
		if embedder == nil {
			return nil, fmt.Errorf("embedding service not configured: enable an embedding model in model management")
		}
		if rs.chunkRepo == nil {
			return nil, fmt.Errorf("hybrid search not available: chunk store not configured")
		}
		legTopK := req.TopK * hybridLegRecallFactor
		if widensRecall(req.Reranking) {
			legTopK = req.TopK * constants.RerankWidenFactor
		}
		vectorResults, pool, err := rs.hybridPool(ctx, req, collectionName, embedder, legTopK)
		if err != nil {
			return nil, err
		}
		result.VectorResults = vectorResults
		sources, rerankErr := rs.rerankSources(ctx, req, pool)
		if rerankErr != nil {
			return nil, rerankErr
		}
		result.Sources = sources
	}

	result.Latency = time.Since(startTime)

	rs.expandParentContext(ctx, req, result)

	rs.logger.Info("RAG query completed",
		zap.String("trace_id", sc.TraceID),
		zap.Int("vector_results", len(result.VectorResults)),
		zap.Duration("latency", result.Latency))

	return result, nil
}

// widensRecall reports whether the selected rerank identity is an external
// provider. External identities widen the internal candidate pool before
// the narrow rerank; builtin and none keep the plain TopK.
func widensRecall(identity string) bool {
	provider, _ := rerankIdentity(identity)
	return provider != "" && provider != "builtin-score-v1"
}

// vectorToPool converts raw vector hits into score-normalised sources,
// mapping Milvus L2 distance to similarity so thresholds and the builtin
// rerank sort uniformly across retrieval modes.
func vectorToPool(results []knowledgeport.VectorSearchResult) []Source {
	pool := make([]Source, 0, len(results))
	for _, vr := range results {
		pool = append(pool, Source{
			DocumentID: vr.SourceDocument,
			ChunkID:    vr.ID,
			Content:    vr.Content,
			ChunkIndex: vr.ChunkIndex,
			Score:      l2ToSim(vr.Score),
		})
	}
	return pool
}

// hybridPool runs both retrieval legs concurrently and fuses them with
// reciprocal rank fusion. A missing collection with no documents falls
// through to the keyword leg alone; other vector failures fail closed.
func (rs *RAGService) hybridPool(ctx context.Context, req RAGQueryRequest, collectionName string, embedder knowledgeport.Embedder, legTopK int) ([]knowledgeport.VectorSearchResult, []Source, error) {
	type vRes struct {
		r []knowledgeport.VectorSearchResult
		e error
	}
	type kRes struct {
		r []domain.Chunk
		e error
	}
	vCh := make(chan vRes, 1)
	kCh := make(chan kRes, 1)
	go func() {
		r, e := rs.queryVector(ctx, req.Question, collectionName, legTopK, embedder, req.EmbeddingModel)
		vCh <- vRes{r, e}
	}()
	go func() {
		if req.WorkspaceID == "" {
			kCh <- kRes{e: fmt.Errorf("keyword search requires workspace ID")}
			return
		}
		r, e := rs.chunkRepo.KeywordSearch(ctx, req.TenantID, req.WorkspaceID, req.Question, legTopK)
		kCh <- kRes{r, e}
	}()
	vr := <-vCh
	kr := <-kCh
	if vr.e != nil {
		if errors.Is(vr.e, errCollectionNotFound) {
			if missingErr := rs.handleMissingCollection(ctx, req); missingErr != nil {
				return nil, nil, missingErr
			}
			// Empty workspace: fall through to the keyword leg alone.
			vr.r = nil
		} else {
			rs.logHybridDependencyFailure(ctx, "hybrid_vector_search")
			return nil, nil, ErrRAGDependency
		}
	}
	if kr.e != nil {
		rs.logHybridDependencyFailure(ctx, "hybrid_keyword_search")
		return nil, nil, ErrRAGDependency
	}
	return vr.r, rrfFuse(vr.r, kr.r), nil
}

// rrfFuse merges both hybrid legs with reciprocal rank fusion, producing a
// score-bearing pool ordered by fused relevance.
func rrfFuse(vectorHits []knowledgeport.VectorSearchResult, keywordHits []domain.Chunk) []Source {
	const rrfK = 60.0
	rrfScores := make(map[string]float64)
	for rank, r := range vectorHits {
		rrfScores[r.ID] += 1.0 / (rrfK + float64(rank+1))
	}
	for rank, c := range keywordHits {
		rrfScores[c.ID] += 1.0 / (rrfK + float64(rank+1))
	}
	srcMap := make(map[string]Source)
	for _, r := range vectorHits {
		srcMap[r.ID] = Source{DocumentID: r.SourceDocument, ChunkID: r.ID,
			Content: r.Content, ChunkIndex: r.ChunkIndex}
	}
	for _, c := range keywordHits {
		if _, ok := srcMap[c.ID]; !ok {
			srcMap[c.ID] = Source{DocumentID: c.DocID, ChunkID: c.ID, Content: c.Text, ChunkIndex: c.Index}
		}
	}
	type scoredSrc struct {
		src   Source
		score float64
	}
	all := make([]scoredSrc, 0, len(rrfScores))
	for id, score := range rrfScores {
		if s, ok := srcMap[id]; ok {
			all = append(all, scoredSrc{s, score})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	pool := make([]Source, 0, len(all))
	for i := range all {
		s := all[i].src
		s.Score = float32(all[i].score)
		pool = append(pool, s)
	}
	return pool
}

// logHybridDependencyFailure records a failed hybrid leg with the query
// trace id so operators can attribute the dependency outage.
func (rs *RAGService) logHybridDependencyFailure(ctx context.Context, operation string) {
	sc, _ := observability.SpanFromContext(ctx)
	rs.logger.Error("knowledge.retrieval.dependency_failed", zap.String("trace_id", sc.TraceID),
		zap.String("operation", operation), zap.String("error_category", "dependency_unavailable"))
}

// expandParentContext attaches parent chunk content for leaf chunks that
// have a parent, giving callers richer context.
func (rs *RAGService) expandParentContext(ctx context.Context, req RAGQueryRequest, result *RAGQueryResult) {
	if rs.chunkRepo == nil || req.WorkspaceID == "" || len(result.Sources) == 0 {
		return
	}
	ids := make([]string, len(result.Sources))
	for i, s := range result.Sources {
		ids[i] = s.ChunkID
	}
	leafChunks, err := rs.chunkRepo.GetChunksByIDs(ctx, req.TenantID, req.WorkspaceID, ids)
	if err != nil {
		return
	}
	parentMap := make(map[string]string) // chunkID → parentID
	for _, lc := range leafChunks {
		if lc.ParentID != "" {
			parentMap[lc.ID] = lc.ParentID
		}
	}
	rs.attachParentContent(ctx, req, result, parentMap)
}

// attachParentContent fills ParentContent for sources whose chunk has a
// parent; missing parents are left untouched.
func (rs *RAGService) attachParentContent(ctx context.Context, req RAGQueryRequest, result *RAGQueryResult, parentMap map[string]string) {
	for i := range result.Sources {
		pid, ok := parentMap[result.Sources[i].ChunkID]
		if !ok {
			continue
		}
		parent, perr := rs.chunkRepo.GetParentByID(ctx, req.TenantID, req.WorkspaceID, pid)
		if perr == nil && parent != nil {
			result.Sources[i].ParentContent = parent.Content
		}
	}
}

// queryVector embeds the question and searches the workspace collection.
// embedModel ("" when unknown) drives the collection dimension check; a
// missing collection yields errCollectionNotFound for the caller to classify.
func (rs *RAGService) queryVector(ctx context.Context, question string, collection string, topK int, embedder knowledgeport.Embedder, embedModel string) ([]knowledgeport.VectorSearchResult, error) {
	rs.logger.Debug("querying vector store")

	if embedder == nil {
		return nil, fmt.Errorf("embedding service not configured: enable an embedding model in model management")
	}

	queryVector, err := embedder.EmbedVector(ctx, question)
	if err != nil {
		return nil, ErrRAGDependency
	}

	if embedModel != "" {
		if err := rs.validateCollectionDim(ctx, collection, embedModel); err != nil {
			return nil, err
		}
	}

	results, err := rs.vectorStore.Search(ctx, collection, queryVector, topK)
	if err != nil {
		if isCollectionNotFound(err) {
			return nil, errCollectionNotFound
		}
		return nil, ErrRAGDependency
	}

	return results, nil
}

// validateCollectionDim checks the live collection schema against the
// embedding model before searching: a dimension mismatch means the workspace
// was (re)created under a different model and must fail closed instead of
// returning silently wrong results. A collection missing the user_id column
// is tolerated (legacy tenant-scoped collection) and only logged.
func (rs *RAGService) validateCollectionDim(ctx context.Context, collection, embedModel string) error {
	info, err := rs.vectorStore.DescribeCollection(ctx, collection)
	if err != nil {
		if isCollectionNotFound(err) {
			return errCollectionNotFound
		}
		rs.logger.Error("knowledge.retrieval.dependency_failed",
			zap.String("operation", "describe_collection"), zap.Error(err))
		return ErrRAGDependency
	}
	if info.Dim != 0 && info.Dim != vectorDim(embedModel) {
		rs.logger.Error("knowledge.retrieval.schema_mismatch",
			zap.String("collection", collection), zap.Int("existing_dim", info.Dim),
			zap.Int("required_dim", vectorDim(embedModel)))
		return ErrRAGDependency
	}
	if !info.HasUserID {
		rs.logger.Warn("collection lacks user_id column, skipping user scope check",
			zap.String("collection", collection))
	}
	return nil
}

// handleMissingCollection classifies a missing vector collection: 0 chunks in
// PG means a legitimately empty workspace (empty result), any chunks means
// drift between PG and Milvus and fails closed.
func (rs *RAGService) handleMissingCollection(ctx context.Context, req RAGQueryRequest) error {
	if rs.chunkRepo == nil {
		return ErrRAGDependency
	}
	count, err := rs.chunkRepo.CountByWorkspace(ctx, req.TenantID, req.WorkspaceID)
	if err != nil {
		rs.logger.Error("knowledge.retrieval.dependency_failed",
			zap.String("operation", "count_chunks"), zap.Error(err))
		return ErrRAGDependency
	}
	if count > 0 {
		rs.logger.Error("knowledge.retrieval.drift",
			zap.Int64("chunk_count", count), zap.String("collection", constants.CollectionName(req.TenantID, req.WorkspaceID)))
		return ErrRAGDependency
	}
	rs.logger.Warn("vector collection not found; workspace has no chunks",
		zap.String("collection", constants.CollectionName(req.TenantID, req.WorkspaceID)))
	return nil
}

// rerankTopK is the final result count after narrowing: RerankTopK when set,
// otherwise TopK.
func rerankTopK(req RAGQueryRequest) int {
	if req.RerankTopK > 0 {
		return req.RerankTopK
	}
	return req.TopK
}

// rerankSources applies the request's rerank strategy to the candidate pool
// and narrows it to the final count. External identities widen the recall
// pool upstream; keyword-mode results never reach here (no scores). Threshold
// filtering applies only to score-bearing sources, so it is safe for vector
// (L2-normalized) and hybrid (RRF) pools alike.
func (rs *RAGService) rerankSources(ctx context.Context, req RAGQueryRequest, pool []Source) ([]Source, error) {
	provider, model := rerankIdentity(req.Reranking)
	switch provider {
	case "builtin-score-v1":
		sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
	case "":
		// no rerank: keep retrieval order
	default:
		narrowed, err := rs.rerankExternal(ctx, req, pool, model)
		if err != nil {
			return nil, err
		}
		pool = narrowed
	}
	if req.ScoreThreshold > 0 {
		pool = filterByScoreThreshold(pool, req.ScoreThreshold)
	}
	if len(pool) > rerankTopK(req) {
		pool = pool[:rerankTopK(req)]
	}
	return pool, nil
}

// rerankExternal re-scores the candidate pool with the configured external
// reranker. Pools below MinRerankCandidates skip the call (stable no-op) to
// avoid paying latency for tiny pools.
func (rs *RAGService) rerankExternal(ctx context.Context, req RAGQueryRequest, pool []Source, model string) ([]Source, error) {
	if rs.reranker == nil {
		return nil, fmt.Errorf("rerank requested (%s) but no external reranker configured", req.Reranking)
	}
	if len(pool) < constants.MinRerankCandidates {
		rs.logger.Warn("rerank skipped: candidate pool too small", zap.Int("pool_size", len(pool)))
		if rs.metrics != nil {
			rs.metrics.IncRerankRequest(req.TenantID, model, "skipped")
		}
		return pool, nil
	}
	if len(pool) > constants.RerankMaxCandidates {
		pool = pool[:constants.RerankMaxCandidates]
	}
	docs := make([]string, len(pool))
	for i, s := range pool {
		docs[i] = s.Content
	}
	results, err := rs.reranker.Rerank(ctx, knowledgeport.RerankRequest{
		Query: req.Question, Documents: docs, Model: model, TopN: rerankTopK(req),
	})
	if err != nil {
		rs.logger.Error("knowledge.retrieval.rerank_failed", zap.Error(err))
		return nil, fmt.Errorf("rerank: %w", err)
	}
	reordered := make([]Source, 0, len(results))
	for _, r := range results {
		if r.Index >= 0 && r.Index < len(pool) {
			s := pool[r.Index]
			s.Score = r.Score
			reordered = append(reordered, s)
		}
	}
	return reordered, nil
}

func filterByScoreThreshold(pool []Source, threshold float32) []Source {
	filtered := make([]Source, 0, len(pool))
	for _, s := range pool {
		if s.Score >= threshold {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// l2ToSim converts a Milvus L2 distance to a similarity score (1/(1+L2)):
// lower distance → higher similarity, so threshold semantics ("keep score >=
// threshold") mean the same thing in every retrieval mode.
func l2ToSim(d float32) float32 {
	return 1.0 / (1.0 + d)
}

func (rs *RAGService) RetrieveRelevantChunks(ctx context.Context, tenantID, question, workspace string, topK int) ([]string, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("knowledge: tenant_id is empty")
	}
	collectionName := constants.CollectionName(tenantID, workspace)

	vectorResults, err := rs.queryVector(ctx, question, collectionName, topK, rs.embeddingSvc, "")
	if err != nil {
		if isCollectionNotFound(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var chunks []string
	for _, result := range vectorResults {
		chunks = append(chunks, result.Content)
	}

	return chunks, nil
}

func (rs *RAGService) BuildPrompt(question string, chunks []string) string {
	var prompt strings.Builder

	prompt.WriteString("Answer the following question based on the provided context:\n\n")
	fmt.Fprintf(&prompt, "Question: %s\n\n", question)

	if len(chunks) > 0 {
		prompt.WriteString("Relevant document chunks:\n")
		for i, chunk := range chunks {
			fmt.Fprintf(&prompt, "%d. %s\n", i+1, chunk)
		}
		prompt.WriteString("\n")
	}

	prompt.WriteString("Provide a clear, accurate answer based on the context above. If the context doesn't contain enough information, say so explicitly.")

	return prompt.String()
}
