package api

import (
	"context"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/mcp"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func NewRouter(cfg *config.Config, registry *orchestrator.Registry, logger *zap.Logger, gateway *llmgateway.Gateway) *gin.Engine {
	router := gin.Default()

	router.Use(middleware.CORSMiddleware())
	router.Use(middleware.ErrorHandler(logger))

	skillHandler := handler.NewSkillHandler(registry, logger, gateway)

	agentRegistry := agent.NewRegistry(registry, gateway, logger)
	agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway)

	parser := document.NewParser(logger)
	chunker := textchunk.NewChunker(logger)
	embeddingSvc := embedding.NewEmbeddingService(cfg.OpenAIAPIKey, logger)
	vectorStore := mcp.NewVectorStore(cfg.MilvusHost, cfg.MilvusPort, logger)
	graphRAG := knowledge.NewGraphRAG(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword, logger)

	ctx := context.Background()
	if err := vectorStore.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Milvus", zap.Error(err))
	}
	if err := graphRAG.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Neo4j", zap.Error(err))
	}

	ingestSvc := knowledge.NewKnowledgeIngest(parser, chunker, embeddingSvc, vectorStore, graphRAG, logger)
	ragService := knowledge.NewRAGService(embeddingSvc, vectorStore, graphRAG, logger)
	ragHandler := handler.NewRAGHandler(ingestSvc, ragService, logger)

	skills := router.Group("/skills")
	{
		skills.POST("", skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
	}

	agents := router.Group("/agents")
	{
		agents.POST("", agentHandler.CreateAgent)
		agents.GET("", agentHandler.ListAgents)
		agents.GET("/:id", agentHandler.GetAgent)
		agents.POST("/:id/execute", agentHandler.ExecuteAgent)
	}

	knowledge := router.Group("/knowledge")
	{
		knowledge.POST("/upload", ragHandler.UploadDocument)
		knowledge.POST("/query", ragHandler.Query)
		knowledge.POST("/workspaces", ragHandler.CreateWorkspace)
		knowledge.GET("/workspaces", ragHandler.ListWorkspaces)
		knowledge.GET("/workspaces/:workspace/stats", ragHandler.GetWorkspaceStats)
		knowledge.DELETE("/workspaces/:workspace", ragHandler.DeleteWorkspace)
	}

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	return router
}
