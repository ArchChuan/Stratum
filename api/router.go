// Package api provides HTTP API router setup and configuration.

package api

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/handler"
	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/knowledge"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/mcp"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/memory"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/orchestrator"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	vectorstore "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// SetupRouter configures and returns the Gin router
func SetupRouter(
	cfg *config.Config,
	logger *zap.Logger,
	registry *orchestrator.Registry,
	gateway *llmgateway.Gateway,
	db *pgxpool.Pool,
	rdb *goredis.Client,
) *gin.Engine {
	router := gin.Default()

	// Observability: single shared PrometheusMetrics instance
	metrics := observability.NewPrometheusMetrics(logger)

	// Inject metrics into LLM gateway
	gateway.WithMetrics(metrics)

	// Middleware
	router.Use(middleware.ErrorHandler(logger))
	router.Use(middleware.CORSMiddleware(cfg.FrontendURL))
	router.Use(middleware.MetricsMiddleware(metrics))

	// Auth setup — only if GitHub OAuth is configured
	var jwtSvc *auth.JWTService
	if cfg.GitHubClientID != "" {
		rsaKey, err := parseRSAPrivateKey(cfg.JWTPrivateKeyPEM)
		if err != nil {
			logger.Warn("JWT private key parse failed, auth routes disabled", zap.Error(err))
		} else {
			jwtSvc = auth.NewJWTService(rsaKey)
			ghClient := auth.NewGitHubClient(cfg.GitHubClientID, cfg.GitHubClientSecret, "", "")
			tokenStore := auth.NewTokenStore(db, rdb)
			onboardSvc := auth.NewOnboardService(db)
			authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
				GitHubClient:  ghClient,
				JWTService:    jwtSvc,
				TokenStore:    tokenStore,
				OnboardSvc:    onboardSvc,
				Logger:        logger,
				CallbackURL:   cfg.GitHubCallbackURL,
				FrontendURL:   cfg.FrontendURL,
				GlobalAdmin:   cfg.GlobalAdminGitHubLogin,
				SecureCookies: cfg.SecureCookies,
			})
			authRoutes := router.Group("/auth")
			{
				authRoutes.GET("/github", authHandler.GitHubLogin)
				authRoutes.GET("/github/callback", authHandler.GitHubCallback)
				authRoutes.POST("/register", authHandler.Register)
				authRoutes.POST("/refresh", authHandler.Refresh)
				authRoutes.POST("/logout", authHandler.Logout)
				authRoutes.GET("/me", authHandler.Me)
				authRoutes.POST("/switch-tenant", authHandler.SwitchTenant)
				authRoutes.POST("/create-tenant", authHandler.CreateUserTenant)
			}

			// Admin routes — require JWT + global_admin role
			if db != nil {
				jwtMW := auth.JWTMiddleware(jwtSvc)
				adminHandler := handler.NewAdminHandler(db, logger)
				tenantHandler := handler.NewTenantHandler(db, logger, cfg.FrontendURL)

				adminGroup := router.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())
				{
					adminGroup.GET("/tenants", adminHandler.ListTenants)
					adminGroup.POST("/tenants", adminHandler.CreateTenant)
					adminGroup.GET("/tenants/:id", adminHandler.GetTenant)
					adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
					adminGroup.DELETE("/tenants/:id", adminHandler.DeleteTenant)
				}

				tenantGroup := router.Group("/tenant", jwtMW, middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
				{
					tenantGroup.GET("/members", tenantHandler.ListMembers)
					tenantGroup.POST("/members/invite", tenantHandler.InviteMember)
					tenantGroup.PATCH("/members/:user_id/role", tenantHandler.UpdateMemberRole)
					tenantGroup.DELETE("/members/:user_id", tenantHandler.RemoveMember)
					tenantGroup.GET("/settings", tenantHandler.GetSettings)
					tenantGroup.PATCH("/settings", tenantHandler.UpdateSettings)
				}

				// /tenant/list only needs JWT, not a specific tenant context.
				router.GET("/tenant/list", jwtMW, tenantHandler.ListUserTenants)
			}
		}
	}
	router.GET("/metrics", gin.WrapH(metrics.GetHandler()))

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

	embedSvc := embedding.NewEmbeddingService(gateway, logger)
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
	ragHandler := handler.NewRAGHandler(ingestSvc, ragService, db, logger)

	// Initialize agent registry and handler
	agentRegistry := agent.NewRegistry(db, logger)
	var execStore *agent.ExecutionStore
	if db != nil {
		execStore = agent.NewExecutionStore(db)
	}
	agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway, metrics, execStore)

	// Initialize memory system
	memoryConfig := memory.DefaultMemoryConfig()
	memoryManager := memory.NewMemoryManager(memoryConfig, logger, nil, nil, nil, db)
	memoryHandler := handler.NewMemoryHandler(memoryManager, logger)

	// Initialize MCP system
	mcpManager := mcp.NewClientManager(logger, nil, db)
	mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
	mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)

	// Restore persisted MCP connections from DB
	if db != nil {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer restoreCancel()
		if err := mcpManager.RestoreFromDB(restoreCtx); err != nil {
			logger.Warn("failed to restore MCP connections from DB", zap.Error(err))
		}
	}

	// requireActive blocks writes when the tenant is suspended.
	requireActive := middleware.RequireActiveTenant(db)

	// Skill endpoints
	skills := router.Group("/skills")
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.POST("", requireActive, skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
		skills.PUT("/:id", requireActive, skillHandler.UpdateSkill)
		skills.DELETE("/:id", requireActive, skillHandler.DeleteSkill)
	}

	// Agent endpoints
	var agentMiddlewares []gin.HandlerFunc
	if jwtSvc != nil {
		agentMiddlewares = append(agentMiddlewares, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext())
	}
	agents := router.Group("/agents", agentMiddlewares...)
	{
		agents.GET("", agentHandler.GetAllAgents)
		agents.POST("", requireActive, agentHandler.CreateAgent)
		agents.GET("/executions", agentHandler.ListExecutions)
		agents.GET("/:id", agentHandler.GetAgent)
		agents.POST("/:id/execute", requireActive, agentHandler.ExecuteAgent)
		agents.DELETE("/:id", requireActive, agentHandler.DeleteAgent)
	}

	// Knowledge endpoints — 所有路由均需 JWT + 租户上下文
	var knowledgeMW []gin.HandlerFunc
	if jwtSvc != nil {
		knowledgeMW = append(knowledgeMW, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
	}
	knowledgeGroup := router.Group("/knowledge", knowledgeMW...)
	{
		// member 可访问（读）
		knowledgeGroup.GET("/workspaces", ragHandler.ListWorkspaces)
		knowledgeGroup.GET("/workspaces/:name/stats", ragHandler.GetWorkspaceStats)
		knowledgeGroup.POST("/query", requireActive, ragHandler.Query)

		// admin/owner 专属写操作（均需 active）
		var adminMW []gin.HandlerFunc
		if jwtSvc != nil {
			adminMW = append(adminMW, middleware.RequireTenantRole("admin"))
		}
		knowledgeGroup.POST("/workspaces", append(adminMW, requireActive, ragHandler.CreateWorkspace)...)
		knowledgeGroup.PATCH("/workspaces/:name", append(adminMW, requireActive, ragHandler.UpdateWorkspace)...)
		knowledgeGroup.DELETE("/workspaces/:name", append(adminMW, requireActive, ragHandler.DeleteWorkspace)...)
		knowledgeGroup.POST("/ingest", append(adminMW, requireActive, ragHandler.UploadDocument)...)
	}

	// Memory endpoints
	var memMiddlewares []gin.HandlerFunc
	if jwtSvc != nil {
		memMiddlewares = append(memMiddlewares, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext())
	}
	mem := router.Group("/memory", memMiddlewares...)
	{
		mem.POST("/sessions", requireActive, memoryHandler.CreateSession)
		mem.POST("", requireActive, memoryHandler.AddMemory)
		mem.GET("/:id", memoryHandler.GetMemory)
		mem.POST("/search", memoryHandler.SearchMemory)
		mem.DELETE("/:id", requireActive, memoryHandler.DeleteMemory)
		mem.GET("/stats", memoryHandler.GetStats)
		mem.DELETE("/session/:session_id", requireActive, memoryHandler.ClearSession)
		mem.GET("/entities", memoryHandler.GetEntities)
		mem.POST("/extract-entities", memoryHandler.ExtractEntities)
		mem.GET("/summary/:session_id", memoryHandler.GetSummary)
	}

	// MCP endpoints
	mcpHandler.RegisterRoutes(router, requireActive)

	return router
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	if pemStr == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_PEM is empty")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key: %w", err)
	}
	return key, nil
}
