// Package http builds the HTTP router from a wiring.Container.
package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
)

// NewRouter assembles the HTTP gin engine from an already-built Container.
// Route registration mirrors the legacy api.SetupRouter exactly so the
// recorded contract goldens continue to PASS.
func NewRouter(c *wiring.Container) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	// Middleware (order matches the legacy api.SetupRouter exactly).
	r.Use(middleware.ErrorHandler(c.Logger))
	r.Use(middleware.TraceMiddleware(c.Logger))
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
	authRoutes := r.Group("/auth")
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
		tenantGroup.POST("/members/invite", tenantHandler.InviteMember)
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

// registerSkills wires /skills/* under JWT + tenant context.
func registerSkills(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	skillHandler := handler.NewSkillHandler(c.Skill.Service, c.Logger)

	var mw []gin.HandlerFunc
	if c.Platform.JWTService != nil {
		mw = append(mw, middleware.JWTMiddleware(c.Platform.JWTService), middleware.InjectTenantContext())
	}
	skills := r.Group("/skills", mw...)
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.POST("", requireActive, skillHandler.CreateSkill)
		skills.GET("/:id", skillHandler.GetSkill)
		skills.PUT("/:id", requireActive, skillHandler.UpdateSkill)
		skills.DELETE("/:id", requireActive, skillHandler.DeleteSkill)
		skills.POST("/:id/run", requireActive, skillHandler.RunSkill)
	}
}

// registerAgents wires /agents/* and /conversations/* under JWT + tenant
// context. Agent + chat handlers share middleware.
func registerAgents(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	agentHandler := handler.NewAgentHandler(c.Agent.Service, c.Logger)
	chatHandler := handler.NewChatHandler(c.Agent.ChatStore, c.Logger)

	var mw []gin.HandlerFunc
	if c.Platform.JWTService != nil {
		mw = append(mw, middleware.JWTMiddleware(c.Platform.JWTService), middleware.InjectTenantContext())
	}
	agents := r.Group("/agents", mw...)
	{
		agents.GET("", agentHandler.GetAllAgents)
		agents.POST("", requireActive, agentHandler.CreateAgent)
		agents.GET("/executions", agentHandler.ListExecutions)
		agents.GET("/:id", agentHandler.GetAgent)
		agents.POST("/:id/execute", requireActive, agentHandler.ExecuteAgent)
		agents.POST("/:id/execute/stream", requireActive, agentHandler.ExecuteAgentStream)
		agents.PUT("/:id", requireActive, agentHandler.UpdateAgent)
		agents.DELETE("/:id", requireActive, agentHandler.DeleteAgent)
		agents.POST("/:id/conversations", chatHandler.CreateConversation)
		agents.GET("/:id/conversations", chatHandler.ListConversations)
	}
	conversations := r.Group("/conversations", mw...)
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

	var mw []gin.HandlerFunc
	if c.Platform.JWTService != nil {
		mw = append(mw, middleware.JWTMiddleware(c.Platform.JWTService), middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
	}
	knowledgeGroup := r.Group("/knowledge", mw...)
	{
		knowledgeGroup.GET("/workspaces", ragHandler.ListWorkspaces)
		knowledgeGroup.GET("/workspaces/:name/stats", ragHandler.GetWorkspaceStats)
		knowledgeGroup.POST("/query", requireActive, ragHandler.Query)

		var adminMW []gin.HandlerFunc
		if c.Platform.JWTService != nil {
			adminMW = append(adminMW, middleware.RequireTenantRole("admin"))
		}
		knowledgeGroup.POST("/workspaces", append(adminMW, requireActive, ragHandler.CreateWorkspace)...)
		knowledgeGroup.PATCH("/workspaces/:name", append(adminMW, requireActive, ragHandler.UpdateWorkspace)...)
		knowledgeGroup.DELETE("/workspaces/:name", append(adminMW, requireActive, ragHandler.DeleteWorkspace)...)
		knowledgeGroup.POST("/ingest", append(adminMW, requireActive, ragHandler.UploadDocument)...)
	}
}

// registerMCP wires /mcp/* via the handler's RegisterRoutes. Write
// routes require JWT + tenant context (same pattern as agents/skills).
func registerMCP(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	mcpHandler := handler.NewMCPHandler(c.MCP.Service, c.Logger)

	var mw []gin.HandlerFunc
	if c.Platform.JWTService != nil {
		mw = append(mw, middleware.JWTMiddleware(c.Platform.JWTService), middleware.InjectTenantContext())
	}
	mcpHandler.RegisterRoutes(r, mw, requireActive)
}

func registerMemory(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Memory == nil || c.Platform.JWTService == nil {
		return
	}

	jwtMW := middleware.JWTMiddleware(c.Platform.JWTService)
	injectTenant := middleware.InjectTenantContext()

	// User-scoped endpoints: authenticated users managing their own memories.
	if c.Memory.Service != nil {
		userHandler := handler.NewUserMemoryHandler(c.Memory.Service)
		userGroup := r.Group("/api/memory", jwtMW, injectTenant, requireActive)
		userGroup.DELETE("/clear", userHandler.ClearMemories)
	}
}
