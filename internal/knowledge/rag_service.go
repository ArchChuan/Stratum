package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
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
	Mode      string // "vector", "graph", "hybrid"
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
	DocumentID    string
	Content      string
	ChunkIndex   int64
	Score        float32
}

type GraphEntity struct {
	ID         string
	Label      string
	Properties map[string]interface{}
}

func (rs *RAGService) Query(ctx context.Context, req RAGQueryRequest) (*RAGQueryResult, error) {
	startTime := time.Now()
	rs.logger.Info("executing RAG query",
		zap.String("question", req.Question),
		zap.String("mode", req.Mode))

	result := &RAGQueryResult{
		Mode:    req.Mode,
		Answer:   "",
		Sources:  []Source{},
		Latency: 0,
	}

	if req.TopK <= 0 {
		req.TopK = 5
	}

	collectionName := fmt.Sprintf("%s_kb", req.Workspace)

	switch req.Mode {
	case "vector":
		vectorResults, err := rs.queryVector(ctx, req.Question, collectionName, req.TopK)
		if err != nil {
			rs.logger.Error("vector query failed", zap.Error(err))
			return nil, fmt.Errorf("vector query failed: %w", err)
		}
		result.VectorResults = vectorResults

		for _, vr := range vectorResults {
			result.Sources = append(result.Sources, Source{
				DocumentID:  vr.ID,
				Content:    vr.Content,
				ChunkIndex: vr.ChunkIndex,
				Score:      vr.Score,
			})
		}

	case "graph":
		graphEntities, err := rs.queryGraph(ctx, req.Question)
		if err != nil {
			rs.logger.Error("graph query failed", zap.Error(err))
			return nil, fmt.Errorf("graph query failed: %w", err)
		}
		result.GraphContext = graphEntities

	case "hybrid":
		vectorResults, err := rs.queryVector(ctx, req.Question, collectionName, req.TopK)
		if err != nil {
			rs.logger.Error("vector query failed", zap.Error(err))
			return nil, fmt.Errorf("vector query failed: %w", err)
		}
		result.VectorResults = vectorResults

		for _, vr := range vectorResults {
			result.Sources = append(result.Sources, Source{
				DocumentID:  vr.ID,
				Content:    vr.Content,
				ChunkIndex: vr.ChunkIndex,
				Score:      vr.Score,
			})
		}

		graphEntities, err := rs.queryGraph(ctx, req.Question)
		if err != nil {
			rs.logger.Warn("graph query failed, using vector only", zap.Error(err))
		} else {
			result.GraphContext = graphEntities
		}
	}

	result.Latency = time.Since(startTime)

	rs.logger.Info("RAG query completed",
		zap.Int("vector_results", len(result.VectorResults)),
		zap.Int("graph_entities", len(result.GraphContext)),
		zap.Duration("latency", result.Latency))

	return result, nil
}

func (rs *RAGService) queryVector(ctx context.Context, question string, collection string, topK int) ([]vector.SearchResult, error) {
	rs.logger.Debug("querying vector store")

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
	rs.logger.Debug("querying knowledge graph")

	entities, err := rs.graphRAG.Query(ctx, question)
	if err != nil {
		return nil, err
	}

	var graphEntities []GraphEntity
	if results, ok := entities.([]interface{}); ok {
		for _, r := range results {
			if m, ok := r.(map[string]interface{}); ok {
				if id, ok := m["id"].(string); ok {
					graphEntities = append(graphEntities, GraphEntity{
						ID:         id,
						Label:      "Entity",
						Properties: m,
					})
				}
			}
		}
	}

	return graphEntities, nil
}

func (rs *RAGService) RetrieveRelevantChunks(ctx context.Context, question string, workspace string, topK int) ([]string, error) {
	collectionName := fmt.Sprintf("%s_kb", workspace)

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
	prompt.WriteString(fmt.Sprintf("Question: %s\n\n", question))

	if len(chunks) > 0 {
		prompt.WriteString("Relevant document chunks:\n")
		for i, chunk := range chunks {
			prompt.WriteString(fmt.Sprintf("%d. %s\n", i+1, chunk))
		}
		prompt.WriteString("\n")
	}

	if len(graphContext) > 0 {
		prompt.WriteString("Knowledge graph context:\n")
		for _, entity := range graphContext {
			prompt.WriteString(fmt.Sprintf("- Entity %s: %v\n", entity.ID, entity.Properties))
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

	results, err := rs.graphRAG.Query(ctx, cypher)
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
