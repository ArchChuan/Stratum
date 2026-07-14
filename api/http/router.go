// Package http builds the HTTP router from a wiring.Container.
package http

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// NewRouter assembles the HTTP gin engine from an already-built Container.
// Route registration mirrors the legacy api.SetupRouter exactly so the
// recorded contract goldens continue to PASS.
func NewRouter(c *wiring.Container) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.BodyLimit(constants.MaxRequestBodyBytes))

	// Middleware (order matches the legacy api.SetupRouter exactly).
	r.Use(middleware.ErrorHandler(c.Logger))
	r.Use(otelgin.Middleware("stratum-ai"))
	r.Use(middleware.TraceMiddleware(c.Logger))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.CORSMiddleware(c.Config.FrontendURL))
	r.Use(middleware.MetricsMiddleware(c.Platform.Metrics))

	requireActive := middleware.RequireActiveTenant(c.DB())

	registerAuth(r, c, requireActive)
	registerHealth(r, c)
	registerSkills(r, c, requireActive)
	registerAgents(r, c, requireActive)
	registerKnowledge(r, c, requireActive)
	registerMCP(r, c, requireActive)
	registerMemory(r, c, requireActive)
	return r
}

// registerAuth wires /auth, /admin/*, /tenant/* routes. JWT-gated groups
// only register when a usable RSA key was provided (Platform.JWTService
// non-nil).
func registerAuth(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	cfg := c.Config
	if cfg.GitHubClientID == "" || c.Platform.JWTService == nil {
		return
	}
	jwtSvc := c.Platform.JWTService

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:      c.Platform.GitHubClient,
		SchemaProvisioner: c.Platform.SchemaProvisioner,
		JWTService:        jwtSvc,
		TokenStore:        c.Platform.TokenStore,
		OnboardSvc:        c.Platform.OnboardSvc,
		Logger:            c.Logger,
		CallbackURL:       cfg.GitHubCallbackURL,
		FrontendURL:       cfg.FrontendURL,
		GlobalAdmin:       cfg.GlobalAdminGitHubLogin,
		SecureCookies:     cfg.SecureCookies,
	})
	authLimiter := middleware.NewRateLimiterStore(middleware.AuthRate, middleware.AuthBurst)
	authRoutes := r.Group("/auth")
	{
		authRoutes.GET("/github", authHandler.GitHubLogin)
		authRoutes.GET("/github/callback", middleware.RateLimit(authLimiter), authHandler.GitHubCallback)
		authRoutes.POST("/register", middleware.RateLimit(authLimiter), authHandler.Register)
		authRoutes.POST("/guest", middleware.RateLimit(authLimiter), authHandler.GuestLogin)
		authRoutes.POST("/refresh", middleware.RateLimit(authLimiter), authHandler.Refresh)
		authRoutes.POST("/logout", authHandler.Logout)
		authRoutes.GET("/me", authHandler.Me)
		authRoutes.POST("/switch-tenant", authHandler.SwitchTenant)
		authRoutes.POST("/create-tenant", authHandler.CreateUserTenant)
	}

	if c.DB() == nil {
		return
	}
	jwtMW := middleware.JWTMiddleware(jwtSvc)
	adminHandler := handler.NewAdminHandler(c.IAM.AdminService, c.Logger)
	tenantHandler := handler.NewTenantHandler(c.IAM.TenantService, c.IAM.AdminService, c.Logger)

	adminGroup := r.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())
	{
		adminGroup.GET("/tenants", adminHandler.ListTenants)
		adminGroup.POST("/tenants", adminHandler.CreateTenant)
		adminGroup.GET("/tenants/:id", adminHandler.GetTenant)
		adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
		adminGroup.DELETE("/tenants/:id", adminHandler.DeleteTenant)
	}

	tenantGroup := r.Group("/tenant", jwtMW, middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
	{
		tenantGroup.GET("/members", tenantHandler.ListMembers)
		tenantGroup.PATCH("/members/:user_id/role", tenantHandler.UpdateMemberRole)
		tenantGroup.DELETE("/members/:user_id", tenantHandler.RemoveMember)
		tenantGroup.GET("/settings", tenantHandler.GetSettings)
		tenantGroup.PATCH("/settings", requireActive, tenantHandler.UpdateSettings)
		tenantGroup.PATCH("/embed-model", requireActive, tenantHandler.SetEmbedModel)
		tenantGroup.DELETE("", middleware.RequireTenantRole("owner"), tenantHandler.DeleteSelf)
	}

	r.GET("/tenant/list", jwtMW, tenantHandler.ListUserTenants)
}

// registerHealth wires /metrics, /health, /models — all unauthenticated.
func registerHealth(r *gin.Engine, c *wiring.Container) {
	r.GET("/metrics", gin.WrapH(c.Platform.Metrics.GetHandler()))
	r.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "Stratum"})
	})
	modelHandler := handler.NewModelHandler(c.LLMGateway.ModelService)
	r.GET("/models", modelHandler.ListModels)
}

func protectedTenantMiddleware(c *wiring.Container, extra ...gin.HandlerFunc) []gin.HandlerFunc {
	if c.Platform == nil || c.Platform.JWTService == nil {
		return []gin.HandlerFunc{func(ctx *gin.Context) {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		}}
	}
	mw := []gin.HandlerFunc{middleware.JWTMiddleware(c.Platform.JWTService), middleware.InjectTenantContext()}
	return append(mw, extra...)
}

// registerSkills wires /skills/* under JWT + tenant context.
func registerSkills(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	skillHandler := handler.NewSkillHandler(c.Skill.Service, c.Logger, c.Skill.VersionService)

	skills := r.Group("/skills", protectedTenantMiddleware(c)...)
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.POST("", requireActive, skillHandler.CreateSkill)
		skills.POST("/test-draft", requireActive, skillHandler.ExecuteDraftSkill)
		skills.GET("/:id/workspace", skillHandler.GetSkillWorkspace)
		skills.GET("/:id", skillHandler.GetSkill)
		skills.PATCH("/:id/draft/capability", requireActive, skillHandler.UpdateDraftCapability)
		skills.PATCH("/:id/draft/contract", requireActive, skillHandler.UpdateDraftContract)
		skills.PATCH("/:id/draft/implementation", requireActive, skillHandler.UpdateDraftImplementation)
		skills.POST("/:id/test", requireActive, skillHandler.ExecuteSkill)
		skills.POST("/:id/publish", requireActive, skillHandler.PublishSkill)
		skills.PUT("/:id", requireActive, skillHandler.UpdateSkill)
		skills.DELETE("/:id", requireActive, skillHandler.DeleteSkill)
	}
}

// registerAgents wires /agents/* and /conversations/* under JWT + tenant
// context. Agent + chat handlers share middleware.
func registerAgents(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	agentHandler := handler.NewAgentHandler(c.Agent.Service, c.Logger)
	chatHandler := handler.NewChatHandler(c.Agent.ChatStore, c.Logger)

	agents := r.Group("/agents", protectedTenantMiddleware(c)...)
	{
		agents.GET("", agentHandler.GetAllAgents)
		agents.POST("", requireActive, agentHandler.CreateAgent)
		agents.GET("/executions", agentHandler.ListExecutions)
		agents.GET("/executions/:traceID/tool-traces", agentHandler.ListExecutionToolTraces)
		agents.GET("/executions/:traceID/trace-events", agentHandler.ListExecutionTraceEvents)
		agents.GET("/:id", agentHandler.GetAgent)
		execLimiter := middleware.NewRateLimiterStore(middleware.LLMExecRate, middleware.LLMExecBurst)
		execRateLimit := middleware.RateLimitByKey(execLimiter, func(c *gin.Context) string {
			tid, _ := c.Get("auth.tenant_id")
			uid, _ := c.Get("auth.sub")
			return fmt.Sprintf("%v:%v", tid, uid)
		})
		agents.POST("/:id/execute", requireActive, execRateLimit, agentHandler.ExecuteAgent)
		agents.POST("/:id/execute/stream", requireActive, execRateLimit, agentHandler.ExecuteAgentStream)
		agents.PUT("/:id", requireActive, agentHandler.UpdateAgent)
		agents.DELETE("/:id", requireActive, agentHandler.DeleteAgent)
		agents.POST("/:id/conversations", chatHandler.CreateConversation)
		agents.GET("/:id/conversations", chatHandler.ListConversations)
	}
	conversations := r.Group("/conversations", protectedTenantMiddleware(c)...)
	{
		conversations.PATCH("/:convID", chatHandler.RenameConversation)
		conversations.DELETE("/:convID", chatHandler.DeleteConversation)
		conversations.GET("/:convID/messages", chatHandler.ListMessages)
		conversations.POST("/:convID/messages", chatHandler.AddMessage)
	}
}

// registerKnowledge wires /knowledge/* under JWT + tenant context with
// member/admin role split for read vs write.
func registerKnowledge(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	ragHandler := handler.NewRAGHandler(c.Knowledge.RAGService, c.Knowledge.WorkspaceService, c.Logger)

	knowledgeGroup := r.Group("/knowledge", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		knowledgeGroup.GET("/workspaces", ragHandler.ListWorkspaces)
		knowledgeGroup.GET("/workspaces/:name/stats", ragHandler.GetWorkspaceStats)
		knowledgeGroup.GET("/workspaces/:name/documents", ragHandler.ListDocuments)
		knowledgeGroup.POST("/query", requireActive, ragHandler.Query)

		adminMW := []gin.HandlerFunc{middleware.RequireTenantRole("admin")}
		knowledgeGroup.POST("/workspaces", append(adminMW, requireActive, ragHandler.CreateWorkspace)...)
		knowledgeGroup.PATCH("/workspaces/:name", append(adminMW, requireActive, ragHandler.UpdateWorkspace)...)
		knowledgeGroup.DELETE("/workspaces/:name", append(adminMW, requireActive, ragHandler.DeleteWorkspace)...)
		knowledgeGroup.POST("/ingest", append(adminMW, requireActive, middleware.BodyLimit(constants.MaxUploadBytes), ragHandler.UploadDocument)...)
	}
}

// registerMCP wires /mcp/* via the handler's RegisterRoutes. Write
// routes require JWT + tenant context (same pattern as agents/skills).
func registerMCP(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	mcpHandler := handler.NewMCPHandler(c.MCP.Service, c.Logger)

	mcpHandler.RegisterRoutes(r, protectedTenantMiddleware(c), requireActive)
}

func registerMemory(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Memory == nil || c.Platform.JWTService == nil {
		return
	}

	jwtMW := middleware.JWTMiddleware(c.Platform.JWTService)
	injectTenant := middleware.InjectTenantContext()

	userHandler := handler.NewUserMemoryHandler(c.Memory.Service, c.Memory.Manager)
	g := r.Group("/memory", jwtMW, injectTenant, requireActive)
	g.DELETE("/clear", userHandler.ClearMemories)
	g.POST("", userHandler.AddMemory)
	g.GET("/:id", userHandler.GetMemory)
	g.POST("/sessions", userHandler.ListSessions)
	g.GET("/stats", userHandler.GetStats)
	g.GET("/summary/:session_id", userHandler.GetSummary)
	g.DELETE("/:id", userHandler.DeleteMemory)
	g.DELETE("/session/:session_id", userHandler.ClearSession)
}
