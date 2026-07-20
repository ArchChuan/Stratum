// Package http builds the HTTP router from a wiring.Container.
package http

import (
	"context"
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
	registerEvaluations(r, c, requireActive)
	registerAgents(r, c, requireActive)
	registerWorkflows(r, c, requireActive)
	registerKnowledge(r, c, requireActive)
	registerMCP(r, c, requireActive)
	registerMemory(r, c, requireActive)
	return r
}

func registerWorkflows(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Workflow == nil || c.Workflow.DefinitionService == nil || c.Workflow.RunService == nil {
		return
	}
	h := handler.NewWorkflowHandlerWithControl(c.Workflow.DefinitionService, c.Workflow.RunService, c.Workflow.ControlService)
	member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	admin := middleware.RequireTenantRole("admin")
	definitions := r.Group("/workflows", member...)
	definitions.GET("/:id", h.GetDefinition)
	definitions.GET("/:id/versions/:versionID", h.GetVersion)
	definitions.POST("", admin, requireActive, h.CreateDefinition)
	definitions.PUT("/:id/draft", admin, requireActive, h.UpdateDefinition)
	definitions.POST("/:id/validate", admin, requireActive, h.ValidateDefinition)
	definitions.POST("/:id/publish", admin, requireActive, h.PublishDefinition)
	startRuns := r.Group("/workflow-runs", member...)
	startRuns.POST("", requireActive, h.StartRun)
	runs := r.Group("/workflow-runs", append(member, admin)...)
	runs.GET("/:id", h.GetRun)
	runs.GET("/:id/events", h.GetEvents)
	runs.GET("/:id/events/stream", h.StreamEvents)
	runs.POST("/:id/cancel", requireActive, h.CancelRun)
	runs.POST("/:id/pause", requireActive, h.PauseRun)
	runs.POST("/:id/resume", requireActive, h.ResumeRun)
	runs.POST("/:id/manual-interventions/:effectID/resolve", requireActive, h.ResolveManual)
	approvals := r.Group("/workflow-approvals", member...)
	approvals.GET("", admin, h.ListApprovals)
	approvals.POST("/:id/decision", admin, requireActive, h.DecideApproval)
}

func registerEvaluations(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Evaluation == nil || c.Evaluation.SuiteService == nil || c.Evaluation.JobService == nil {
		return
	}
	h := handler.NewEvaluationHandler(
		c.Evaluation.SuiteService, c.Evaluation.JobService, c.Evaluation.Service,
		c.Evaluation.OptimizationService, c.Evaluation.ExperimentService,
		c.Evaluation.FeedbackService, c.Logger,
	)
	requireAdmin := middleware.RequireTenantRole("admin")
	evaluations := r.Group("/evaluations", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		evaluations.POST("/suites", requireAdmin, requireActive, h.CreateSuite)
		evaluations.POST("/suites/:id/publish", requireAdmin, requireActive, h.PublishSuite)
		evaluations.POST("/runs", requireAdmin, requireActive, h.EnqueueRun)
		evaluations.GET("/runs/:id", requireAdmin, h.GetRun)
		evaluations.GET("/jobs/:id", requireAdmin, h.GetJob)
		evaluations.POST("/optimizations", requireAdmin, requireActive, h.GenerateOptimization)
		evaluations.POST("/experiments", requireAdmin, requireActive, h.CreateExperiment)
		evaluations.POST("/experiments/:id/evaluate", requireAdmin, requireActive, h.EvaluateExperiment)
		evaluations.POST("/feedback", requireActive, h.RecordFeedback)
	}
}

// registerAuth wires /auth, /admin/*, /tenant/* routes. JWT-gated groups
// only register when a usable RSA key was provided (Platform.JWTService
// non-nil).
func registerAuth(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	cfg := c.Config
	if c.Platform.JWTService == nil {
		return
	}
	jwtSvc := c.Platform.JWTService

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:       c.Platform.GitHubClient,
		SchemaProvisioner:  c.Platform.SchemaProvisioner,
		JWTService:         jwtSvc,
		TokenStore:         c.Platform.TokenStore,
		OAuthExchangeStore: c.Platform.OAuthExchangeStore,
		OnboardSvc:         c.Platform.OnboardSvc,
		Logger:             c.Logger,
		CallbackURL:        cfg.GitHubCallbackURL,
		FrontendURL:        cfg.FrontendURL,
		GlobalAdmin:        cfg.GlobalAdminGitHubLogin,
		SecureCookies:      cfg.SecureCookies,
	})
	authLimiter := middleware.NewRateLimiterStore(middleware.AuthRate, middleware.AuthBurst)
	authRoutes := r.Group("/auth")
	{
		if cfg.GitHubClientID != "" && c.Platform.GitHubClient != nil {
			authRoutes.GET("/github", authHandler.GitHubLogin)
			authRoutes.GET("/github/callback", middleware.RateLimit(authLimiter), authHandler.GitHubCallback)
		}
		authRoutes.POST("/register", middleware.RateLimit(authLimiter), authHandler.Register)
		authRoutes.POST("/oauth/exchange", middleware.RateLimit(authLimiter), authHandler.OAuthExchange)
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
	r.GET("/livez", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", readinessHandler(c.ReadinessCheck))
	r.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "Stratum"})
	})
	modelHandler := handler.NewModelHandler(c.LLMGateway.ModelService)
	r.GET("/models", modelHandler.ListModels)
}

func readinessHandler(check func(context.Context) map[string]error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if check == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), constants.RouterHealthTimeout)
		defer cancel()
		for _, err := range check(ctx) {
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
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

// registerSkills wires versioned instruction bundles. Skills are activated by
// the Agent loop; they are never executed directly through an HTTP endpoint.
func registerSkills(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	skillHandler := handler.NewSkillHandler(c.Skill.VersionService, c.Logger)

	skills := r.Group("/skills", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		skills.GET("", skillHandler.GetAllSkills)
		skills.GET("/:id/workspace", skillHandler.GetSkillWorkspace)
		skills.GET("/:id", skillHandler.GetSkill)

		adminMW := []gin.HandlerFunc{middleware.RequireTenantRole("admin")}
		skills.POST("", append(adminMW, requireActive, skillHandler.CreateSkill)...)
		skills.PATCH("/:id/draft/capability", append(adminMW, requireActive, skillHandler.UpdateDraftCapability)...)
		skills.PATCH("/:id/draft/activation", append(adminMW, requireActive, skillHandler.UpdateDraftActivation)...)
		skills.PATCH("/:id/draft/instructions", append(adminMW, requireActive, skillHandler.UpdateDraftInstructionBundle)...)
		skills.POST("/:id/publish", append(adminMW, requireActive, skillHandler.PublishSkill)...)
		skills.DELETE("/:id", append(adminMW, requireActive, skillHandler.DeleteSkill)...)
	}
}

// registerAgents wires /agents/* and /conversations/* under JWT + tenant
// context. Agent + chat handlers share middleware. Read + execute + chat
// stay open to members; create/update/delete require admin so ordinary
// tenant members can only use agents, not modify them.
func registerAgents(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	agentHandler := handler.NewAgentHandler(c.Agent.Service, c.Logger)
	chatHandler := handler.NewChatHandler(c.Agent.ChatStore, c.Logger)

	requireAdmin := middleware.RequireTenantRole("admin")

	agents := r.Group("/agents", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		agents.GET("", agentHandler.GetAllAgents)
		agents.POST("", requireAdmin, requireActive, agentHandler.CreateAgent)
		agents.GET("/executions", agentHandler.ListExecutions)
		agents.GET("/executions/:traceID/tool-traces", agentHandler.ListExecutionToolTraces)
		agents.GET("/executions/:traceID/trace-events", agentHandler.ListExecutionTraceEvents)
		agents.GET("/tool-approvals", agentHandler.ListToolApprovals)
		agents.POST("/tool-approvals/:approvalID/decision", requireAdmin, requireActive, agentHandler.DecideToolApproval)
		agents.POST("/tool-approvals/:approvalID/resume", requireAdmin, requireActive, agentHandler.ResumeToolApproval)
		agents.GET("/:id", agentHandler.GetAgent)
		execLimiter := middleware.NewRateLimiterStore(middleware.LLMExecRate, middleware.LLMExecBurst)
		execRateLimit := middleware.RateLimitByKey(execLimiter, func(c *gin.Context) string {
			tid, _ := c.Get("auth.tenant_id")
			uid, _ := c.Get("auth.sub")
			return fmt.Sprintf("%v:%v", tid, uid)
		})
		agents.POST("/:id/execute", requireActive, execRateLimit, agentHandler.ExecuteAgent)
		agents.POST("/:id/execute/stream", requireActive, execRateLimit, agentHandler.ExecuteAgentStream)
		agents.PUT("/:id", requireAdmin, requireActive, agentHandler.UpdateAgent)
		agents.DELETE("/:id", requireAdmin, requireActive, agentHandler.DeleteAgent)
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
		knowledgeGroup.DELETE("/workspaces/:name/documents/:documentID", append(adminMW, requireActive, ragHandler.DeleteDocument)...)
		knowledgeGroup.POST("/ingest", append(adminMW, requireActive, middleware.BodyLimit(constants.MaxUploadBytes), ragHandler.UploadDocument)...)
	}
}

// registerMCP wires /mcp/* via the handler's RegisterRoutes.
//   - base:  JWT + tenant context + member 底线（所有路由，含读取与工具执行）。
//   - write: member 可执行的运行时操作追加 requireActive（工具执行）。
//   - admin: 服务器管理类操作（连接/更新/断开/删除配置/重连/刷新技能）要求 admin+。
func registerMCP(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	mcpHandler := handler.NewMCPHandler(c.MCP.Service, c.Logger)

	base := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	writeMW := []gin.HandlerFunc{requireActive}
	adminMW := []gin.HandlerFunc{middleware.RequireTenantRole("admin"), requireActive}

	mcpHandler.RegisterRoutes(r, base, writeMW, adminMW)
}

func registerMemory(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Memory == nil || c.Platform.JWTService == nil {
		return
	}

	userHandler := handler.NewUserMemoryHandler(c.Memory.Service, c.Memory.Manager)
	g := r.Group("/memory", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	g.Use(requireActive)
	g.DELETE("/clear", userHandler.ClearMemories)
	g.POST("", userHandler.AddMemory)
	g.GET("/:id", userHandler.GetMemory)
	g.POST("/sessions", userHandler.ListSessions)
	g.GET("/stats", userHandler.GetStats)
	g.GET("/summary/:session_id", userHandler.GetSummary)
	g.DELETE("/:id", userHandler.DeleteMemory)
	g.DELETE("/session/:session_id", userHandler.ClearSession)
}
