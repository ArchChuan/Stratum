// Package api provides HTTP API router setup and configuration.

package api

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/byteBuilderX/stratum/api/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent"
	"github.com/byteBuilderX/stratum/internal/auth"
	"github.com/byteBuilderX/stratum/internal/capgateway"
	"github.com/byteBuilderX/stratum/internal/config"
	"github.com/byteBuilderX/stratum/internal/document"
	"github.com/byteBuilderX/stratum/internal/embedding"
	"github.com/byteBuilderX/stratum/internal/knowledge"
	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/internal/mcp"
	"github.com/byteBuilderX/stratum/internal/memory"
	"github.com/byteBuilderX/stratum/internal/memory/pipeline"
	"github.com/byteBuilderX/stratum/internal/skill"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
	vectorstore "github.com/byteBuilderX/stratum/pkg/vector"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// SetupRouter configures and returns the Gin router
func SetupRouter(
	cfg *config.Config,
	logger *zap.Logger,
	gateway *llmgateway.Gateway,
	db *pgxpool.Pool,
	rdb *goredis.Client,
	capGW capgateway.CapabilityGateway,
	skillAdapter capgateway.Adapter,
	memPipeline *pipeline.Pipeline,
) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	// Observability: single shared PrometheusMetrics instance
	metrics := observability.NewPrometheusMetrics(logger)

	aesKey := pkgcrypto.DeriveAESKey(cfg.JWTPrivateKeyPEM)
	gatewayCache := llmgateway.NewTenantGatewayCache()
	requireActive := middleware.RequireActiveTenant(db)

	// Inject metrics into LLM gateway
	gateway.WithMetrics(metrics)

	// Middleware
	router.Use(middleware.ErrorHandler(logger))
	router.Use(middleware.TraceMiddleware(logger))
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
				Pool:          db,
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
				tenantHandler := handler.NewTenantHandler(db, logger, cfg.FrontendURL, aesKey, gatewayCache)

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
					tenantGroup.PATCH("/settings", requireActive, tenantHandler.UpdateSettings)
					tenantGroup.PATCH("/embed-model", requireActive, tenantHandler.SetEmbedModel)
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
			"service": "Stratum",
		})
	})

	// Available chat models (no auth required)
	modelHandler := handler.NewModelHandler(gateway)
	router.GET("/models", modelHandler.ListModels)

	// Initialize services
	vectorStore := vectorstore.NewVectorStore(cfg.MilvusHost, cfg.MilvusPort, logger)
	graphRAG := knowledge.NewGraphRAG(cfg.Neo4jURI, cfg.Neo4jUser, cfg.Neo4jPassword, logger)

	var embedSvc *embedding.EmbeddingService
	if gateway.HasEmbeddingClient() {
		embedSvc = embedding.NewEmbeddingService(gateway, logger)
	} else {
		logger.Info("no global embedding client, will resolve per-tenant at runtime")
	}
	parser := document.NewParser(logger)
	chunker := textchunk.NewChunker(logger)

	// Connect to external services with timeout
	ctx, cancel := context.WithTimeout(context.Background(), constants.RouterHealthTimeout)
	defer cancel()

	if err := vectorStore.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Milvus", zap.Error(err))
	}
	if err := graphRAG.Connect(ctx); err != nil {
		logger.Warn("failed to connect to Neo4j", zap.Error(err))
	}

	// Knowledge services
	ingestSvc := knowledge.NewKnowledgeIngest(parser, chunker, embedSvc, vectorStore, graphRAG, logger)
	if db != nil {
		ingestSvc.SetEmbedResolver(buildKnowledgeEmbedResolver(db, gatewayCache, aesKey, logger))
	}
	ragService := knowledge.NewRAGService(embedSvc, vectorStore, graphRAG, logger)

	// Handlers
	codeExecutor := skill.NewCodeExecutor(skill.DefaultCodeExecutorConfig())
	skillHandler := handler.NewSkillHandler(db, logger, gateway, codeExecutor)
	ragHandler := handler.NewRAGHandler(ingestSvc, ragService, db, logger)

	// Initialize agent registry and handler
	agentRegistry := agent.NewRegistry(db, logger)
	if capGW != nil {
		agentRegistry.SetCapGateway(capGW)
	}
	if db != nil {
		memInjector := pipeline.NewMemoryInjector(db, logger, nil, vectorStore)
		memInjector.SetEmbedResolver(buildEmbedResolver(db, gatewayCache, aesKey, logger))
		agentRegistry.SetMemoryInjector(memInjector)
		if memPipeline != nil {
			memPipeline.SetEmbedResolver(buildEmbedResolver(db, gatewayCache, aesKey, logger))
		}
	}
	var execStore *agent.ExecutionStore
	var chatStore agent.ChatStore
	if db != nil {
		execStore = agent.NewExecutionStore(db)
		chatStore = agent.NewPgChatStore(db)
	}
	// Initialize MCP system
	mcpManager := mcp.NewClientManager(logger, nil, db)
	mcpRegistry := mcp.NewMCPSkillRegistry(mcpManager, logger)
	mcpHandler := handler.NewMCPHandler(mcpRegistry, mcpManager, logger)

	if db != nil {
		if err := mcpManager.RestoreFromDB(ctx); err != nil {
			logger.Warn("failed to restore MCP servers from DB", zap.Error(err))
		}
		// RegisterServer is intentionally NOT called here for all restored clients.
		// MCP tools are per-agent: only servers mounted to an agent are injected
		// at execution time via buildExtraTools (agent_handler.go), which calls
		// GetAdapterForServer lazily — adapter is created on first access.
		// Bulk pre-registration would expose all tenant MCP tools to every agent.
	}

	agentHandler := handler.NewAgentHandler(agentRegistry, logger, gateway, metrics, execStore, db, aesKey, gatewayCache, ragService, mcpRegistry, skillAdapter, chatStore)
	chatHandler := handler.NewChatHandler(chatStore, logger)

	// Initialize memory system
	memoryConfig := memory.DefaultMemoryConfig()
	memoryManager := memory.NewMemoryManager(memoryConfig, logger, nil, nil, nil, db)
	memoryHandler := handler.NewMemoryHandler(memoryManager, logger)

	// Skill endpoints — JWT + InjectTenantContext required (same pattern as agents)
	var skillMW []gin.HandlerFunc
	if jwtSvc != nil {
		skillMW = append(skillMW, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext())
	}
	skills := router.Group("/skills", skillMW...)
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.POST("", requireActive, skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
		skills.PUT("/:id", requireActive, skillHandler.UpdateSkill)
		skills.DELETE("/:id", requireActive, skillHandler.DeleteSkill)
		skills.POST("/:id/run", requireActive, skillHandler.RunSkill)
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
		agents.POST("/:id/execute/stream", requireActive, agentHandler.ExecuteAgentStream)
		agents.PUT("/:id", requireActive, agentHandler.UpdateAgent)
		agents.DELETE("/:id", requireActive, agentHandler.DeleteAgent)
		// Chat conversation endpoints scoped to an agent
		agents.POST("/:id/conversations", chatHandler.CreateConversation)
		agents.GET("/:id/conversations", chatHandler.ListConversations)
	}

	// Conversation-level endpoints (not agent-scoped)
	conversations := router.Group("/conversations", agentMiddlewares...)
	{
		conversations.PATCH("/:convID", chatHandler.RenameConversation)
		conversations.DELETE("/:convID", chatHandler.DeleteConversation)
		conversations.GET("/:convID/messages", chatHandler.ListMessages)
		conversations.POST("/:convID/messages", chatHandler.AddMessage)
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

	// MCP endpoints — write routes need JWT + tenant context (same pattern as agents/skills)
	var mcpWriteMW []gin.HandlerFunc
	if jwtSvc != nil {
		mcpWriteMW = append(mcpWriteMW, auth.JWTMiddleware(jwtSvc), middleware.InjectTenantContext())
	}
	mcpWriteMW = append(mcpWriteMW, requireActive)
	mcpHandler.RegisterRoutes(router, mcpWriteMW...)

	return router
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	if pemStr == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_PEM is empty")
	}
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
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

// buildEmbedResolver creates a per-tenant EmbedServiceResolver that resolves
// embedding capability from tenant DB settings via the gateway cache.
func buildEmbedResolver(db *pgxpool.Pool, cache *llmgateway.TenantGatewayCache, aesKey [32]byte, logger *zap.Logger) pipeline.EmbedServiceResolver {
	return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
		// Read settings first so embed_model is available on both cache-hit and miss paths.
		var settingsJSON []byte
		if err := db.QueryRow(ctx,
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&settingsJSON); err != nil {
			return nil
		}
		var settings map[string]interface{}
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			return nil
		}
		embedModel, _ := settings["embed_model"].(string)

		if gw, _, ok := cache.Get(tenantID); ok && gw.HasEmbeddingClient() {
			m := embedModel
			if m == "" {
				m = gw.DefaultEmbeddingModel()
			}
			return embedding.NewEmbeddingServiceWithModel(gw, m, logger)
		}

		apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
		if !ok || len(apiKeysRaw) == 0 {
			return nil
		}

		decrypted := make(map[string]string, len(apiKeysRaw))
		for provider, enc := range apiKeysRaw {
			encStr, ok := enc.(string)
			if !ok || encStr == "" {
				continue
			}
			plain, err := pkgcrypto.Decrypt(aesKey, encStr)
			if err != nil {
				continue
			}
			decrypted[provider] = plain
		}
		if len(decrypted) == 0 {
			return nil
		}

		gw := llmgateway.NewGateway().WithLogger(logger)
		if qwenKey, ok := decrypted["qwen"]; ok {
			qwenClient := llmgateway.NewQwenClient(qwenKey, logger)
			gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
			gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
		}
		if zhipuKey, ok := decrypted["zhipu"]; ok {
			zhipuClient := llmgateway.NewZhipuClient(zhipuKey, logger)
			gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
			gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
		}
		for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
			if _, ok := decrypted[string(pref)]; ok {
				gw.SetDefault(pref)
				break
			}
		}

		if !gw.HasEmbeddingClient() {
			return nil
		}
		cache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)
		m := embedModel
		if m == "" {
			m = gw.DefaultEmbeddingModel()
		}
		return embedding.NewEmbeddingServiceWithModel(gw, m, logger)
	}
}

// buildKnowledgeEmbedResolver returns a knowledge.EmbedResolver that resolves
// the embedding client for a given tenant, honouring the workspace-level model.
func buildKnowledgeEmbedResolver(db *pgxpool.Pool, cache *llmgateway.TenantGatewayCache, aesKey [32]byte, logger *zap.Logger) knowledge.EmbedResolver {
	return func(ctx context.Context, tenantID, model string) knowledge.EmbedClient {
		// Try gateway cache first.
		if gw, _, ok := cache.Get(tenantID); ok && gw.HasEmbeddingClient() {
			m := model
			if m == "" {
				m = gw.DefaultEmbeddingModel()
			}
			return embedding.NewEmbeddingServiceWithModel(gw, m, logger)
		}

		// Fall back to tenant DB settings to build gateway.
		var settingsJSON []byte
		if err := db.QueryRow(ctx,
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&settingsJSON); err != nil {
			return nil
		}
		var settings map[string]interface{}
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			return nil
		}

		apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
		if !ok || len(apiKeysRaw) == 0 {
			return nil
		}

		decrypted := make(map[string]string, len(apiKeysRaw))
		for provider, enc := range apiKeysRaw {
			encStr, ok := enc.(string)
			if !ok || encStr == "" {
				continue
			}
			plain, err := pkgcrypto.Decrypt(aesKey, encStr)
			if err != nil {
				continue
			}
			decrypted[provider] = plain
		}
		if len(decrypted) == 0 {
			return nil
		}

		gw := llmgateway.NewGateway().WithLogger(logger)
		if qwenKey, ok := decrypted["qwen"]; ok {
			qwenClient := llmgateway.NewQwenClient(qwenKey, logger)
			gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
			gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
		}
		if zhipuKey, ok := decrypted["zhipu"]; ok {
			zhipuClient := llmgateway.NewZhipuClient(zhipuKey, logger)
			gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
			gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
		}
		for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
			if _, ok := decrypted[string(pref)]; ok {
				gw.SetDefault(pref)
				break
			}
		}

		cache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)
		m := model
		if m == "" {
			m = gw.DefaultEmbeddingModel()
		}
		return embedding.NewEmbeddingServiceWithModel(gw, m, logger)
	}
}
