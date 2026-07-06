// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/vector"
	"go.uber.org/zap"
)

// NewRAGSearchFn returns a knowledge search function suitable for the agent's
// WithRAGSearchFn hook. It fans out across workspaces concurrently and
// concatenates results; the first error is returned only when no content was
// produced.
func NewRAGSearchFn(rs *RAGService, tenantID string) func(
	ctx context.Context, workspaces []string, query string, topK int,
) (string, error) {
	return func(ctx context.Context, workspaces []string, query string, topK int) (string, error) {
		type wsResult struct {
			content string
			err     error
		}
		results := make([]wsResult, len(workspaces))
		var wg sync.WaitGroup
		for i, ws := range workspaces {
			wg.Add(1)
			go func(i int, ws string) {
				defer wg.Done()
				mode := "hybrid"
				effectiveTopK := topK
				embedModel := ""
				if rs.wsRepo != nil {
					if cfg, err := rs.wsRepo.GetConfigByID(ctx, tenantID, ws); err == nil {
						if cfg.TopK > 0 {
							effectiveTopK = cfg.TopK
						}
						embedModel = cfg.EmbeddingModel
					}
				}
				out, err := rs.Query(ctx, RAGQueryRequest{
					WorkspaceID:    ws,
					Question:       query,
					TenantID:       tenantID,
					Mode:           mode,
					TopK:           effectiveTopK,
					EmbeddingModel: embedModel,
				})
				if err != nil {
					results[i] = wsResult{err: err}
					return
				}
				var sb strings.Builder
				for _, src := range out.Sources {
					sb.WriteString(src.Content)
					sb.WriteString("\n---\n")
				}
				results[i] = wsResult{content: sb.String()}
			}(i, ws)
		}
		wg.Wait()
		var combined strings.Builder
		var firstErr error
		for _, r := range results {
			if r.err != nil && firstErr == nil {
				firstErr = r.err
				continue
			}
			combined.WriteString(r.content)
		}
		if combined.Len() == 0 && firstErr != nil {
			return "", firstErr
		}
		return combined.String(), nil
	}
}

type RAGService struct {
	embeddingSvc  knowledgeport.Embedder
	embedResolver EmbedResolver
	wsRepo        knowledgeport.WorkspaceRepo
	chunkRepo     knowledgeport.ChunkRepo
	vectorStore   *vector.VectorStore
	logger        *zap.Logger
}

func NewRAGService(
	embeddingSvc knowledgeport.Embedder,
	vectorStore *vector.VectorStore,
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
}

type RAGQueryResult struct {
	Answer        string
	Sources       []Source
	VectorResults []vector.SearchResult
	Mode          string
	Latency       time.Duration
}

type Source struct {
	DocumentID string
	Content    string
	ChunkIndex int64
	Score      float32
}

func (rs *RAGService) Query(ctx context.Context, req RAGQueryRequest) (*RAGQueryResult, error) {
	startTime := time.Now()
	sc, _ := observability.SpanFromContext(ctx)
	rs.logger.Info("executing RAG query",
		zap.String("trace_id", sc.TraceID),
		zap.String("question", req.Question),
		zap.String("mode", req.Mode))

	result := &RAGQueryResult{
		Mode:    req.Mode,
		Answer:  "",
		Sources: []Source{},
		Latency: 0,
	}

	if req.TopK <= 0 {
		req.TopK = 5
	}

	if req.WorkspaceID == "" && req.Workspace != "" && rs.wsRepo != nil {
		ws, err := rs.wsRepo.GetByName(ctx, req.TenantID, req.Workspace)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace: %w", err)
		}
		req.WorkspaceID = ws.ID
		if req.EmbeddingModel == "" {
			req.EmbeddingModel = ws.Config.EmbeddingModel
		}
	}

	collectionName := constants.CollectionName(req.TenantID, req.WorkspaceID)

	switch req.Mode {
	case "vector":
		vectorResults, err := rs.queryVector(ctx, req.Question, collectionName, req.TopK, rs.resolveEmbedder(ctx, req))
		if err != nil {
			rs.logger.Error("vector query failed", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("vector query failed: %w", err)
		}
		result.VectorResults = vectorResults

		for _, vr := range vectorResults {
			result.Sources = append(result.Sources, Source{
				DocumentID: vr.ID,
				Content:    vr.Content,
				ChunkIndex: vr.ChunkIndex,
				Score:      vr.Score,
			})
		}

	case "keyword":
		if rs.chunkRepo == nil {
			return nil, fmt.Errorf("keyword search not available: chunk store not configured")
		}
		if req.WorkspaceID == "" {
			return nil, fmt.Errorf("keyword search requires workspace ID")
		}
		chunks, err := rs.chunkRepo.KeywordSearch(ctx, req.TenantID, req.WorkspaceID, req.Question, req.TopK)
		if err != nil {
			rs.logger.Error("keyword query failed", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("keyword query failed: %w", err)
		}
		for _, c := range chunks {
			result.Sources = append(result.Sources, Source{
				DocumentID: c.ID,
				Content:    c.Text,
				ChunkIndex: c.Index,
			})
		}

	case "hybrid":
		embedder := rs.resolveEmbedder(ctx, req)
		if embedder == nil {
			return nil, fmt.Errorf("embedding service not configured: set an embedding model in tenant settings")
		}
		if rs.chunkRepo == nil {
			return nil, fmt.Errorf("hybrid search not available: chunk store not configured")
		}
		type vRes struct {
			r []vector.SearchResult
			e error
		}
		type kRes struct {
			r []domain.Chunk
			e error
		}
		vCh := make(chan vRes, 1)
		kCh := make(chan kRes, 1)
		go func() {
			r, e := rs.queryVector(ctx, req.Question, collectionName, req.TopK*2, embedder)
			vCh <- vRes{r, e}
		}()
		go func() {
			if req.WorkspaceID == "" {
				kCh <- kRes{e: fmt.Errorf("keyword search requires workspace ID")}
				return
			}
			r, e := rs.chunkRepo.KeywordSearch(ctx, req.TenantID, req.WorkspaceID, req.Question, req.TopK*2)
			kCh <- kRes{r, e}
		}()
		vr := <-vCh
		kr := <-kCh
		if vr.e != nil {
			rs.logger.Error("hybrid vector search failed", zap.String("trace_id", sc.TraceID), zap.Error(vr.e))
			return nil, fmt.Errorf("vector search failed: %w", vr.e)
		}
		if kr.e != nil {
			rs.logger.Error("hybrid keyword search failed", zap.String("trace_id", sc.TraceID), zap.Error(kr.e))
			return nil, fmt.Errorf("keyword search failed: %w", kr.e)
		}
		const rrfK = 60.0
		rrfScores := make(map[string]float64)
		for rank, r := range vr.r {
			rrfScores[r.ID] += 1.0 / (rrfK + float64(rank+1))
		}
		for rank, c := range kr.r {
			rrfScores[c.ID] += 1.0 / (rrfK + float64(rank+1))
		}
		srcMap := make(map[string]Source)
		for _, r := range vr.r {
			srcMap[r.ID] = Source{DocumentID: r.ID, Content: r.Content, ChunkIndex: r.ChunkIndex}
		}
		for _, c := range kr.r {
			if _, ok := srcMap[c.ID]; !ok {
				srcMap[c.ID] = Source{DocumentID: c.ID, Content: c.Text, ChunkIndex: c.Index}
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
		topN := req.TopK
		if topN > len(all) {
			topN = len(all)
		}
		for i := 0; i < topN; i++ {
			s := all[i].src
			s.Score = float32(all[i].score)
			result.Sources = append(result.Sources, s)
		}
	}

	result.Latency = time.Since(startTime)

	rs.logger.Info("RAG query completed",
		zap.String("trace_id", sc.TraceID),
		zap.Int("vector_results", len(result.VectorResults)),
		zap.Duration("latency", result.Latency))

	return result, nil
}

func (rs *RAGService) queryVector(ctx context.Context, question string, collection string, topK int, embedder knowledgeport.Embedder) ([]vector.SearchResult, error) {
	rs.logger.Debug("querying vector store")

	if embedder == nil {
		return nil, fmt.Errorf("embedding service not configured: set an embedding model in tenant settings")
	}

	queryVector, err := embedder.EmbedVector(ctx, question)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	results, err := rs.vectorStore.Search(ctx, collection, queryVector, topK)
	if err != nil {
		if strings.Contains(err.Error(), "collection not found") {
			rs.logger.Warn("vector collection not found, skipping", zap.String("collection", collection))
			return nil, nil
		}
		return nil, err
	}

	return results, nil
}

func (rs *RAGService) RetrieveRelevantChunks(ctx context.Context, question string, workspace string, topK int) ([]string, error) {
	tenantID := ""
	if tc, ok := pgcontext.FromContext(ctx); ok {
		tenantID = tc.TenantID
	}
	collectionName := constants.CollectionName(tenantID, workspace)

	vectorResults, err := rs.queryVector(ctx, question, collectionName, topK, rs.embeddingSvc)
	if err != nil {
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
