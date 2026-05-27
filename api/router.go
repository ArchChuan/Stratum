package api

import (
	"context"
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/memory"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
	vectorstore "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SetupRouter configures and returns the Gin router
func SetupRouter(
	cfg *config.Config,
	logger *zap.Logger,
	registry *orchestrator.Registry,
	gateway *llmgateway.Gateway,
) *gin.Engine {
	router := gin.Default()

	// Middleware
	router.Use(middleware.ErrorHandler(logger))

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "ClawHermes AI Go",
		})
	})

	// Initialize services
	vectorStore := vectorstore.NewVectorStore(cfg.MilvusHost, cfg.MilvusPort, logger)
	graphRAG := knowledge.NewGraphRAG(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword, logger)

	embedSvc := embedding.NewEmbeddingService(cfg.OpenAIAPIKey, logger)
	parser := document.NewParser(logger)
	chunker := textchunk.NewChunker(logger)

	// Connect to external services with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := vectorStore.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Milvus", zap.Error(err))
	}
	if err := graphRAG.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Neo4j", zap.Error(err))
	}

	// Knowledge services
	ingestSvc := knowledge.NewKnowledgeIngest(parser, chunker, embedSvc, vectorStore, graphRAG, logger)
	ragService := knowledge.NewRAGService(embedSvc, vectorStore, graphRAG, logger)

	// Handlers
	skillHandler := handler.NewSkillHandler(registry, logger, gateway)
	ragHandler := handler.NewRAGHandler(ingestSvc, ragService, logger)

	// Initialize agent registry and handler
	agentRegistry := agent.NewRegistry(logger)
	agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway)

	// Initialize memory system
	memoryConfig := memory.DefaultMemoryConfig()
	memoryManager := memory.NewMemoryManager(memoryConfig, logger, nil, nil, nil)
	memoryHandler := handler.NewMemoryHandler(memoryManager, logger)

	// Initialize MCP system
	mcpManager := mcp.NewClientManager(logger, nil)
	mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
	mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)

	// Skill endpoints
	skills := router.Group("/skills")
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.POST("", skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
		skills.PUT("/:id", skillHandler.UpdateSkill)
		skills.DELETE("/:id", skillHandler.DeleteSkill)
	}

	// Agent endpoints
	agents := router.Group("/agents")
	{
		agents.GET("", agentHandler.GetAllAgents)
		agents.POST("", agentHandler.CreateAgent)
		agents.GET("/:id", agentHandler.GetAgent)
		agents.POST("/:id/execute", agentHandler.ExecuteAgent)
		agents.DELETE("/:id", agentHandler.DeleteAgent)
	}

	// Knowledge endpoints
	knowledge := router.Group("/knowledge")
	{
		knowledge.POST("/ingest", ragHandler.UploadDocument)
		knowledge.POST("/query", ragHandler.Query)
	}

	// Memory endpoints
	mem := router.Group("/memory")
	{
		mem.POST("/sessions", memoryHandler.CreateSession)
		mem.POST("", memoryHandler.AddMemory)
		mem.GET("/:id", memoryHandler.GetMemory)
		mem.POST("/search", memoryHandler.SearchMemory)
		mem.DELETE("/:id", memoryHandler.DeleteMemory)
		mem.GET("/stats", memoryHandler.GetStats)
		mem.DELETE("/session/:session_id", memoryHandler.ClearSession)
		mem.GET("/entities", memoryHandler.GetEntities)
		mem.POST("/extract-entities", memoryHandler.ExtractEntities)
		mem.GET("/summary/:session_id", memoryHandler.GetSummary)
	}

	// MCP endpoints
	mcpHandler.RegisterRoutes(router)

	return router
}
