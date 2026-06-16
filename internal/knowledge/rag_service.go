// Package knowledge provides knowledge base and RAG services.
package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/embedding"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/byteBuilderX/stratum/pkg/vector"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j/dbtype"
	"go.uber.org/zap"
)

type RAGService struct {
	embeddingSvc *embedding.EmbeddingService
	vectorStore  *vector.VectorStore
	graphRAG     *GraphRAG
	logger       *zap.Logger
}

func NewRAGService(
	embeddingSvc *embedding.EmbeddingService,
	vectorStore *vector.VectorStore,
	graphRAG *GraphRAG,
	logger *zap.Logger,
) *RAGService {
	return &RAGService{
		embeddingSvc: embeddingSvc,
		vectorStore:  vectorStore,
		graphRAG:     graphRAG,
		logger:       logger,
	}
}

type RAGQueryRequest struct {
	Question  string
	Workspace string
	TenantID  string
	Mode      string // "vector", "keyword", "graph", "hybrid"
	TopK      int
}

type RAGQueryResult struct {
	Answer        string
	Sources       []Source
	GraphContext  []GraphEntity
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

type GraphEntity struct {
	ID         string
	Label      string
	Properties map[string]interface{}
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

	collectionName := fmt.Sprintf("%s_kb", req.Workspace)
	if col, err := tenantdb.WorkspaceCollection(ctx, req.Workspace); err == nil {
		collectionName = col
	}

	switch req.Mode {
	case "vector":
		vectorResults, err := rs.queryVector(ctx, req.Question, collectionName, req.TopK)
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
		keywordResults, err := rs.queryKeyword(ctx, req.Question, collectionName, req.TopK)
		if err != nil {
			rs.logger.Error("keyword query failed", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("keyword query failed: %w", err)
		}
		result.VectorResults = keywordResults

		for _, kr := range keywordResults {
			result.Sources = append(result.Sources, Source{
				DocumentID: kr.ID,
				Content:    kr.Content,
				ChunkIndex: kr.ChunkIndex,
				Score:      kr.Score,
			})
		}

	case "graph":
		graphEntities, err := rs.queryGraph(ctx, req.Question)
		if err != nil {
			rs.logger.Error("graph query failed", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("graph query failed: %w", err)
		}
		result.GraphContext = graphEntities

	case "hybrid":
		// Hybrid = Vector + Keyword (RRF fusion)
		if rs.embeddingSvc == nil {
			return nil, fmt.Errorf("embedding service not configured: set an embedding model in tenant settings")
		}

		queryVector, err := rs.embeddingSvc.EmbedVector(ctx, req.Question)
		if err != nil {
			rs.logger.Error("failed to embed query", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("failed to embed query: %w", err)
		}

		hybridResults, err := rs.vectorStore.HybridSearch(ctx, collectionName, queryVector, req.Question, req.TopK)
		if err != nil {
			rs.logger.Error("hybrid query failed", zap.String("trace_id", sc.TraceID), zap.Error(err))
			return nil, fmt.Errorf("hybrid query failed: %w", err)
		}
		result.VectorResults = hybridResults

		for _, hr := range hybridResults {
			result.Sources = append(result.Sources, Source{
				DocumentID: hr.ID,
				Content:    hr.Content,
				ChunkIndex: hr.ChunkIndex,
				Score:      hr.Score,
			})
		}
	}

	result.Latency = time.Since(startTime)

	rs.logger.Info("RAG query completed",
		zap.String("trace_id", sc.TraceID),
		zap.Int("vector_results", len(result.VectorResults)),
		zap.Int("graph_entities", len(result.GraphContext)),
		zap.Duration("latency", result.Latency))

	return result, nil
}

func (rs *RAGService) queryVector(ctx context.Context, question string, collection string, topK int) ([]vector.SearchResult, error) {
	rs.logger.Debug("querying vector store")

	if rs.embeddingSvc == nil {
		return nil, fmt.Errorf("embedding service not configured: set an embedding model in tenant settings")
	}

	queryVector, err := rs.embeddingSvc.EmbedVector(ctx, question)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	results, err := rs.vectorStore.Search(ctx, collection, queryVector, topK)
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (rs *RAGService) queryGraph(ctx context.Context, question string) ([]GraphEntity, error) {
	sc, _ := observability.SpanFromContext(ctx)
	rs.logger.Debug("querying knowledge graph", zap.String("trace_id", sc.TraceID))

	records, err := rs.graphRAG.FullTextSearch(ctx, question, 20)
	if err != nil {
		return nil, err
	}

	var graphEntities []GraphEntity
	for _, m := range records {
		raw, ok := m["node"]
		if !ok {
			continue
		}
		n, ok := raw.(dbtype.Node)
		if !ok {
			rs.logger.Warn("unexpected node type", zap.String("trace_id", sc.TraceID), zap.String("type", fmt.Sprintf("%T", raw)))
			continue
		}
		id, _ := n.Props["id"].(string)
		if id == "" {
			rs.logger.Warn("graph search result missing id, skipping", zap.String("trace_id", sc.TraceID))
			continue
		}
		label := "Entity"
		if len(n.Labels) > 0 {
			label = n.Labels[0]
		}
		graphEntities = append(graphEntities, GraphEntity{
			ID:         id,
			Label:      label,
			Properties: n.Props,
		})
	}

	return graphEntities, nil
}

func (rs *RAGService) queryKeyword(ctx context.Context, question string, collection string, topK int) ([]vector.SearchResult, error) {
	rs.logger.Debug("querying keyword store")

	results, err := rs.vectorStore.KeywordSearch(ctx, collection, question, topK)
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (rs *RAGService) RetrieveRelevantChunks(ctx context.Context, question string, workspace string, topK int) ([]string, error) {
	collectionName := fmt.Sprintf("%s_kb", workspace)
	if col, err := tenantdb.WorkspaceCollection(ctx, workspace); err == nil {
		collectionName = col
	}

	vectorResults, err := rs.queryVector(ctx, question, collectionName, topK)
	if err != nil {
		return nil, err
	}

	var chunks []string
	for _, result := range vectorResults {
		chunks = append(chunks, result.Content)
	}

	return chunks, nil
}

func (rs *RAGService) BuildPrompt(question string, chunks []string, graphContext []GraphEntity) string {
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

	if len(graphContext) > 0 {
		prompt.WriteString("Knowledge graph context:\n")
		for _, entity := range graphContext {
			fmt.Fprintf(&prompt, "- Entity %s: %v\n", entity.ID, entity.Properties)
		}
		prompt.WriteString("\n")
	}

	prompt.WriteString("Provide a clear, accurate answer based on the context above. If the context doesn't contain enough information, say so explicitly.")

	return prompt.String()
}

func (rs *RAGService) GetWorkspaceCollections(ctx context.Context) ([]string, error) {
	rs.logger.Debug("getting workspace collections")

	cypher := `
		MATCH (d:Document)
		WITH d.workspace as workspace
		RETURN DISTINCT workspace
		ORDER BY workspace
	`

	results, err := rs.graphRAG.Query(ctx, cypher, nil)
	if err != nil {
		return nil, err
	}

	var workspaces []string
	if resultList, ok := results.([]interface{}); ok {
		for _, r := range resultList {
			if workspace, ok := r.(map[string]interface{}); ok {
				if ws, ok := workspace["workspace"].(string); ok {
					workspaces = append(workspaces, ws)
				}
			}
		}
	}

	return workspaces, nil
}
