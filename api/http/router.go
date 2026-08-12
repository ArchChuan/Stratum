// Package http builds the HTTP router from a wiring.Container.
package http

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"golang.org/x/time/rate"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/internal/iam/application"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// NewRouter assembles the HTTP gin engine from an already-built Container.
// Route registration mirrors the legacy api.SetupRouter exactly so the
// recorded contract goldens continue to PASS.
func NewRouter(c *wiring.Container) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.BodyLimit(constants.MaxRequestBodyBytes))

	// Trace wraps error rendering so its access log observes the final status.
	r.Use(otelgin.Middleware("stratum-ai"))
	r.Use(middleware.TraceMiddleware(c.Logger))
	r.Use(middleware.ErrorHandler(c.Logger))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.CORSMiddleware(c.Config.FrontendURL))
	r.Use(middleware.MetricsMiddleware(c.Platform.Metrics))
	if c.Audit != nil && c.Audit.Recorder != nil {
		r.Use(middleware.AuditMiddleware(c.Audit.Recorder))
	}

	requireActive := middleware.RequireActiveTenant(c.DB())

	registerAuth(r, c, requireActive)
	registerModelCatalogue(r, c)
	registerDashboard(r, c)
	registerHealth(r, c)
	registerSkills(r, c, requireActive)
	registerEvaluations(r, c, requireActive)
	registerAgents(r, c, requireActive)
	registerResourceChangeProposals(r, c, requireActive)
	registerOperationProposals(r, c, requireActive)
	registerWorkflows(r, c, requireActive)
	registerCollab(r, c, requireActive)
	registerScheduledTasks(r, c, requireActive)
	registerKnowledge(r, c, requireActive)
	registerMCP(r, c, requireActive)
	registerMemory(r, c, requireActive)
	registerAudit(r, c, requireActive)
	registerMechanism(r, c, requireActive)
	registerPrompt(r, c, requireActive)
	registerLLMAdmin(r, c, requireActive)
	if c.Config.AvatarDir != "" {
		r.GET("/avatars/:filename", func(ctx *gin.Context) {
			ctx.File(filepath.Join(c.Config.AvatarDir, ctx.Param("filename")))
		})
	}
	return r
}

func registerDashboard(r *gin.Engine, c *wiring.Container) {
	if c.Platform == nil || c.Platform.DashboardService == nil {
		return
	}
	h := handler.NewDashboardHandler(c.Platform.DashboardService)
	dashboard := r.Group("/dashboard", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	dashboard.GET("/overview", h.Overview)
}

func registerOperationProposals(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Agent == nil || c.Agent.OperationProposalSvc == nil || c.Agent.OperationGateService == nil {
		return
	}
	h := handler.NewOperationProposalHandler(c.Agent.OperationProposalSvc, c.Agent.Service)
	member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	admin := protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"))
	routes := r.Group("/operation-proposals", admin...)
	routes.Use(requireActive)
	routes.GET("", h.List)
	routes.GET("/:id", h.Get)
	routes.POST("/:id/review", h.Review)
	routes.POST("/:id/approve", h.Approve)
	routes.POST("/:id/reject", h.Reject)
	// The member-facing gated mutation channel sits on the agent resource;
	// the operation gate always proposes for self-modify, so no budget or
	// delegation fields are accepted here.
	agents := r.Group("/agents/:id/self-modify", member...)
	agents.Use(requireActive)
	agents.POST("", h.SelfModify)
}

func registerResourceChangeProposals(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Agent == nil || c.Agent.ProposalService == nil {
		return
	}
	h := handler.NewResourceChangeProposalHandler(c.Agent.ProposalService)
	routes := r.Group("/resource-change-proposals",
		protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"))...)
	routes.Use(requireActive)
	routes.GET("/:id", h.Get)
	routes.PATCH("/:id", h.Update)
	routes.POST("/:id/cancel", h.Cancel)
	routes.POST("/:id/confirm", h.Confirm)
}

func registerWorkflows(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Workflow == nil || c.Workflow.DefinitionService == nil || c.Workflow.RunService == nil {
		return
	}
	h := handler.NewWorkflowHandlerWithControl(c.Workflow.DefinitionService, c.Workflow.RunService, c.Workflow.ControlService)
	member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	admin := middleware.RequireTenantRole("admin")
	definitions := r.Group("/workflows", member...)
	definitions.GET("", h.ListDefinitions)
	definitions.GET("/:id", h.GetDefinition)
	definitions.GET("/:id/versions", h.ListVersions)
	definitions.GET("/:id/versions/:versionID", h.GetVersion)
	definitions.POST("", admin, requireActive, h.CreateDefinition)
	definitions.PUT("/:id/draft", admin, requireActive, h.UpdateDefinition)
	definitions.DELETE("/:id", admin, requireActive, h.DeleteDefinition)
	definitions.POST("/:id/validate", admin, requireActive, h.ValidateDefinition)
	definitions.POST("/:id/publish", admin, requireActive, h.PublishDefinition)
	startRuns := r.Group("/workflow-runs", member...)
	startRuns.POST("", requireActive, h.StartRun)
	runs := r.Group("/workflow-runs", member...)
	runs.GET("", h.ListRuns)
	runs.GET("/:id", h.GetRun)
	runs.GET("/:id/events", h.GetEvents)
	runs.GET("/:id/events/stream", h.StreamEvents)
	runs.POST("/:id/cancel", requireActive, h.CancelRun)
	runs.POST("/:id/pause", admin, requireActive, h.PauseRun)
	runs.POST("/:id/resume", admin, requireActive, h.ResumeRun)
	runs.POST("/:id/manual-interventions/:effectID/resolve", admin, requireActive, h.ResolveManual)
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
		c.Evaluation.FeedbackService, c.Evaluation.QueryService, c.Evaluation.CandidateService,
		c.Logger,
	).WithBaselineService(c.Evaluation.BaselineService).WithAgentRevisionApplier(c.Evaluation.AgentRevisionApplier).
		WithTestCaseGenerator(c.Evaluation.TestCaseGenerator)
	requireAdmin := middleware.RequireTenantRole("admin")
	evaluations := r.Group("/evaluations", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		// 读端点收 admin（D6）：评估中心数据（runs/candidates/overview/jobs）暴露
		// 检索质量与实验结果，仅管理员可读；其余只读资源仍对 member 开放。
		evaluations.GET("/overview", requireAdmin, h.Overview)
		evaluations.GET("/resources", h.ListResources)
		evaluations.GET("/suites", h.ListSuites)
		evaluations.GET("/runs", requireAdmin, h.ListRuns)
		evaluations.GET("/candidates", requireAdmin, h.ListCandidates)
		evaluations.GET("/experiments", h.ListExperiments)
		evaluations.GET("/resources/:kind/:id/timeline", h.Timeline)
		evaluations.POST("/resources/:kind/:id/baseline", requireAdmin, requireActive, h.CreateBaseline)
		evaluations.POST("/suites", requireAdmin, requireActive, h.CreateSuite)
		evaluations.POST("/suites/:id/publish", requireAdmin, requireActive, h.PublishSuite)
		evaluations.POST("/suites/:id/generate", requireAdmin, requireActive, h.GenerateSuiteCases)
		evaluations.GET("/suites/:id/draft", requireAdmin, requireActive, h.GetSuiteDraft)
		evaluations.PUT("/suites/:id/draft/cases/:caseId", requireAdmin, requireActive, h.UpdateDraftCase)
		evaluations.POST("/runs", requireAdmin, requireActive, h.EnqueueRun)
		evaluations.GET("/runs/:id", requireAdmin, h.GetRun)
		evaluations.GET("/jobs/:id", requireAdmin, h.GetJob)
		evaluations.POST("/optimizations", requireAdmin, requireActive, h.GenerateOptimization)
		evaluations.POST("/experiments", requireAdmin, requireActive, h.CreateExperiment)
		evaluations.POST("/candidates/:id/reject", requireAdmin, requireActive, h.RejectCandidate)
		evaluations.POST("/experiments/:id/pause", requireAdmin, requireActive, h.PauseExperiment)
		evaluations.POST("/experiments/:id/promote", requireAdmin, requireActive, h.PromoteExperiment)
		evaluations.POST("/experiments/:id/rollback", requireAdmin, requireActive, h.RollbackExperiment)
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
	var invitationSvc *application.InvitationService
	if c.IAM != nil {
		invitationSvc = c.IAM.InvitationService
	}

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:       c.Platform.GitHubClient,
		SchemaProvisioner:  c.Platform.SchemaProvisioner,
		JWTService:         jwtSvc,
		TokenStore:         c.Platform.TokenStore,
		OAuthExchangeStore: c.Platform.OAuthExchangeStore,
		OnboardSvc:         c.Platform.OnboardSvc,
		InvitationSvc:      invitationSvc,
		Logger:             c.Logger,
		GitHubAuthorizeURL: cfg.GitHubAuthorizeURL,
		CallbackURL:        cfg.GitHubCallbackURL,
		FrontendURL:        cfg.FrontendURL,
		GlobalAdmin:        cfg.GlobalAdminGitHubLogin,
		SecureCookies:      cfg.SecureCookies,
		GuestAuthEnabled:   cfg.GuestAuthEnabled,
		AvatarStore:        c.Platform.AvatarStore,
	})
	authLimiter := newRateLimiterStore(c, middleware.AuthRate, middleware.AuthBurst)
	authRoutes := r.Group("/auth")
	{
		if cfg.GitHubClientID != "" && c.Platform.GitHubClient != nil {
			authRoutes.GET("/github", authHandler.GitHubLogin)
			authRoutes.GET("/github/callback", middleware.RateLimit(authLimiter), authHandler.GitHubCallback)
		}
		authRoutes.POST("/register", middleware.RateLimit(authLimiter), authHandler.Register)
		if cfg.PasswordAuthEnabled {
			authRoutes.POST("/password/register", middleware.RateLimit(authLimiter), authHandler.UsernameRegister)
			authRoutes.POST("/password/login", middleware.RateLimit(authLimiter), authHandler.UsernameLogin)
		}
		authRoutes.POST("/oauth/exchange", middleware.RateLimit(authLimiter), authHandler.OAuthExchange)
		authRoutes.POST("/guest", middleware.RateLimit(authLimiter), authHandler.GuestLogin)
		authRoutes.POST("/refresh", middleware.RateLimit(authLimiter), authHandler.Refresh)
		authRoutes.POST("/logout", authHandler.Logout)
		authRoutes.GET("/me", authHandler.Me)
		authRoutes.PATCH("/me", authHandler.UpdateProfile)
		authRoutes.POST("/me/avatar", authHandler.UploadAvatar)
		authRoutes.POST("/switch-tenant", authHandler.SwitchTenant)
		authRoutes.POST("/create-tenant", authHandler.CreateUserTenant)
	}

	if c.IAM == nil || c.IAM.AdminService == nil || c.IAM.TenantService == nil {
		return
	}
	jwtMW := middleware.JWTMiddleware(jwtSvc, c.Platform.Metrics)
	adminHandler := handler.NewAdminHandler(c.IAM.AdminService, c.Logger)
	tenantHandler := handler.NewTenantHandler(c.IAM.TenantService, c.IAM.InvitationService, c.IAM.AdminService, c.Logger)

	adminGroup := r.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())
	{
		adminGroup.GET("/tenants", adminHandler.ListTenants)
		adminGroup.POST("/tenants", adminHandler.CreateTenant)
		adminGroup.GET("/tenants/:id", adminHandler.GetTenant)
		adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
		adminGroup.DELETE("/tenants/:id", adminHandler.DeleteTenant)
		registerParameterAdminRoutes(adminGroup, c)
		registerMemoryDLQAdminRoutes(adminGroup, c)
	}

	tenantGroup := r.Group("/tenant", jwtMW, middleware.InjectTenantContext(), middleware.RequireTenantRole("member"))
	{
		tenantGroup.GET("/members", tenantHandler.ListMembers)
		tenantGroup.POST("/members/invite", requireActive, tenantHandler.InviteMember)
		tenantGroup.POST("/join", tenantHandler.JoinTenant)
		tenantGroup.PATCH("/members/:user_id/role", tenantHandler.UpdateMemberRole)
		tenantGroup.DELETE("/members/:user_id", tenantHandler.RemoveMember)
		tenantGroup.GET("/settings", tenantHandler.GetSettings)
		tenantGroup.PATCH("/settings", requireActive, tenantHandler.UpdateSettings)
		tenantGroup.DELETE("", middleware.RequireTenantRole("owner"), tenantHandler.DeleteSelf)
	}

	r.GET("/tenant/list", jwtMW, tenantHandler.ListUserTenants)
}

// registerParameterAdminRoutes wires the unified parameter registry admin
// endpoints (schema + platform values) when the registry is wired.
func registerParameterAdminRoutes(adminGroup *gin.RouterGroup, c *wiring.Container) {
	if c.Parameters == nil || c.Parameters.Service == nil {
		return
	}
	paramHandler := handler.NewParameterHandler(c.Parameters.Service, c.Logger)
	adminGroup.GET("/parameters/schema", paramHandler.Schema)
	adminGroup.GET("/parameters", paramHandler.List)
	adminGroup.PUT("/parameters", paramHandler.Update)
}

// dlqReplayAdapter 把 pipeline.ReplayService 适配到 handler 的消费方接口
// （router 层是 wiring 之外的唯一允许适配点，避免 wiring import handler）。
type dlqReplayAdapter struct {
	svc *pipeline.ReplayService
}

func (a dlqReplayAdapter) ReplayByErrorCode(ctx context.Context, errorCode string) (handler.MemoryDLQReplayResult, error) {
	result, err := a.svc.ReplayByErrorCode(ctx, errorCode)
	return handler.MemoryDLQReplayResult{
		Total: result.Total, Replayed: result.Replayed, Skipped: result.Skipped, Failed: result.Failed,
	}, err
}

// registerMemoryDLQAdminRoutes wires POST /admin/memory/dlq/replay on the
// global admin group when the memory pipeline (and its NATS connection) is up.
func registerMemoryDLQAdminRoutes(adminGroup *gin.RouterGroup, c *wiring.Container) {
	if c.Memory == nil || c.Memory.DLQReplay == nil {
		return
	}
	h := handler.NewMemoryDlqReplayHandler(dlqReplayAdapter{svc: c.Memory.DLQReplay})
	adminGroup.POST("/memory/dlq/replay", h.Replay)
}

func registerModelCatalogue(r *gin.Engine, c *wiring.Container) {
	if c.LLMGateway == nil {
		return
	}
	modelHandler := handler.NewModelHandler(c.LLMGateway.ModelService)
	models := r.Group("/models", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	models.GET("", modelHandler.ListModels)
}

// registerHealth wires unauthenticated observability and health endpoints.
func registerHealth(r *gin.Engine, c *wiring.Container) {
	r.GET("/metrics", gin.WrapH(c.Platform.Metrics.GetHandler()))
	r.GET("/livez", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", readinessHandler(c.ReadinessCheck))
	r.GET("/health", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "service": "Stratum"})
	})
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
	mw := []gin.HandlerFunc{middleware.JWTMiddleware(c.Platform.JWTService, c.Platform.Metrics), middleware.InjectTenantContext()}
	return append(mw, extra...)
}

// registerSkills wires versioned instruction bundles. Skills are activated by
// the Agent loop; they are never executed directly through an HTTP endpoint.
func registerSkills(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Skill == nil || c.Skill.VersionService == nil {
		return
	}
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
		skills.PUT("/:id/editors", append(adminMW, requireActive, skillHandler.SetSkillEditors)...)
	}
}

// registerAgents wires /agents/* and /conversations/* under JWT + tenant
// context. Agent + chat handlers share middleware. Read + execute + chat
// stay open to members; create/update/delete require admin so ordinary
// tenant members can only use agents, not modify them.
func registerAgents(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Agent == nil || c.Agent.Service == nil {
		return
	}
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
		agents.GET("/system/settings", agentHandler.GetSettings)
		agents.PUT("/system/settings", requireAdmin, requireActive, agentHandler.UpdateModel)
		agents.GET("/:id", agentHandler.GetAgent)
		execLimiter := newRateLimiterStore(c, middleware.LLMExecRate, middleware.LLMExecBurst)
		execRateLimit := middleware.RateLimitByKey(execLimiter, func(c *gin.Context) string {
			tid, _ := c.Get("auth.tenant_id")
			uid, _ := c.Get("auth.sub")
			return fmt.Sprintf("%v:%v", tid, uid)
		})
		agents.POST("/:id/execute", requireActive, execRateLimit, agentHandler.ExecuteAgent)
		agents.POST("/:id/execute/stream", requireActive, execRateLimit, agentHandler.ExecuteAgentStream)
		agents.POST("/:id/executions/:executionID/pause", requireActive, agentHandler.PauseExecution)
		agents.POST("/:id/executions/:executionID/resume", requireActive, agentHandler.ResumeExecution)
		agents.PUT("/:id", requireAdmin, requireActive, agentHandler.UpdateAgent)
		agents.PUT("/:id/editors", requireAdmin, requireActive, agentHandler.SetAgentEditors)
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

func newRateLimiterStore(c *wiring.Container, limit rate.Limit, burst int) *middleware.RateLimiterStore {
	if c.Storage != nil && c.Storage.Redis != nil {
		return middleware.NewRedisRateLimiterStore(c.Storage.Redis.Client(), limit, burst)
	}
	return middleware.NewRateLimiterStore(limit, burst)
}

// registerCollab wires /collaborations/* for all members. Start/cancel
// authorization (creator vs admin/owner) is enforced by the service.
func registerCollab(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Collab == nil || c.Collab.Service == nil {
		return
	}
	h := handler.NewCollaborationHandler(c.Collab.Service)
	member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	routes := r.Group("/collaborations", member...)
	routes.Use(requireActive)
	routes.GET("", h.List)
	routes.POST("", h.Create)
	routes.GET("/:id", h.Get)
	routes.POST("/:id/start", h.Start)
	routes.POST("/:id/cancel", h.Cancel)
}

// registerScheduledTasks wires /scheduled-tasks/*: reads are member-level,
// writes (create/update/delete/enable) require admin plus an active tenant.
// Params use :id so scripts/record-contracts.go resolves them as named paths.
func registerScheduledTasks(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Scheduler == nil || c.Scheduler.Service == nil {
		return
	}
	h := handler.NewScheduledTaskHandler(c.Scheduler.Service)
	member := protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))
	admin := append(protectedTenantMiddleware(c, middleware.RequireTenantRole("admin")), requireActive)
	group := r.Group("/scheduled-tasks", member...)
	{
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.POST("", append(admin, h.Create)...)
		group.PUT("/:id", append(admin, h.Update)...)
		group.DELETE("/:id", append(admin, h.Delete)...)
		group.PATCH("/:id/enabled", append(admin, h.SetEnabled)...)
	}
}

// registerKnowledge wires /knowledge/* under JWT + tenant context with
// member/admin role split for read vs write.
func registerKnowledge(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Knowledge == nil || c.Knowledge.RAGService == nil {
		return
	}
	ragHandler := handler.NewRAGHandler(c.Knowledge.RAGService, c.Knowledge.WorkspaceService, c.Logger)

	knowledgeGroup := r.Group("/knowledge", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		knowledgeGroup.GET("/workspaces", ragHandler.ListWorkspaces)
		knowledgeGroup.GET("/workspaces/:name/stats", ragHandler.GetWorkspaceStats)
		knowledgeGroup.GET("/workspaces/:name/documents", ragHandler.ListDocuments)
		knowledgeGroup.GET("/workspaces/:name/documents/:documentID/preview", requireActive, ragHandler.PreviewDocument)
		knowledgeGroup.POST("/query", requireActive, ragHandler.Query)

		adminMW := []gin.HandlerFunc{middleware.RequireTenantRole("admin")}
		knowledgeGroup.POST("/workspaces", append(adminMW, requireActive, ragHandler.CreateWorkspace)...)
		knowledgeGroup.PATCH("/workspaces/:name", append(adminMW, requireActive, ragHandler.UpdateWorkspace)...)
		knowledgeGroup.DELETE("/workspaces/:name", append(adminMW, requireActive, ragHandler.DeleteWorkspace)...)
		knowledgeGroup.PUT("/workspaces/:name/editors", append(adminMW, requireActive, ragHandler.SetWorkspaceEditors)...)
		knowledgeGroup.DELETE("/workspaces/:name/documents/:documentID", append(adminMW, requireActive, ragHandler.DeleteDocument)...)
		knowledgeGroup.PUT("/workspaces/:name/documents/:documentID/access",
			append(adminMW, requireActive, ragHandler.SetDocumentAccess)...)
		knowledgeGroup.POST("/ingest", append(adminMW, requireActive, middleware.BodyLimit(constants.MaxUploadBytes), ragHandler.UploadDocument)...)
	}
}

// registerMCP wires /mcp/* via the handler's RegisterRoutes.
//   - base:  JWT + tenant context + member 底线（所有路由，含读取与工具执行）。
//   - write: member 可执行的运行时操作追加 requireActive（工具执行）。
//   - admin: 服务器管理类操作（连接/更新/断开/删除配置/重连/刷新技能）要求 admin+。
func registerMCP(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.MCP == nil || c.MCP.Service == nil {
		return
	}
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

	// LLMGateway 可能未构建（DB 不可用），handler 内部对 nil resolver fail-closed。
	var embedSvc handler.DefaultEmbedModelResolver
	if c.LLMGateway != nil {
		embedSvc = c.LLMGateway.Registry
	}
	userHandler := handler.NewUserMemoryHandler(c.Memory.Service, c.Memory.Manager, embedSvc)
	g := r.Group("/memory", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	g.Use(requireActive)
	g.DELETE("/clear", userHandler.ClearMemories)
	g.GET("", userHandler.ListMemories)
	g.POST("/sessions", userHandler.ListSessions)
	g.GET("/stats", userHandler.GetStats)
	g.GET("/entities", userHandler.GetEntities)
	g.GET("/summary/:session_id", userHandler.GetSummary)
	g.DELETE("/session/:session_id", userHandler.ClearSession)
}

// registerLLMAdmin wires /admin/providers and /admin/models under JWT + tenant
// context with the admin role. These routes are only registered when the
// LLMGateway is fully built (DB available).
func registerAudit(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Audit == nil || c.Audit.QueryService == nil {
		return
	}
	h := handler.NewAuditHandler(c.Audit.QueryService, c.Logger)
	auditGroup := r.Group("/audit", protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"))...)
	auditGroup.Use(requireActive)
	auditGroup.GET("/events", h.ListEvents)
	auditGroup.GET("/events/:id", h.GetEvent)
}

// registerMechanism wires /mechanism/profiles — 机制基线（model_profiles）
// 管理面。整体依附默认租户（RequireDefaultTenant）：只有 tenant_default 的
// admin/owner/root 可管理，普通租户经消费路径透明取用同一份档案、零感知。
func registerMechanism(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Mechanism == nil || c.Mechanism.Service == nil {
		return
	}
	h := handler.NewMechanismHandler(c.Mechanism.Service, c.Mechanism.Matrix, c.Logger)
	// 管理面整体 admin + 默认租户：普通租户（含其 admin）一律 403。
	profiles := r.Group("/mechanism/profiles",
		protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"), middleware.RequireDefaultTenant())...)
	profiles.Use(requireActive)
	{
		profiles.GET("", h.List)
		profiles.GET("/:familyKey", h.Get)
		profiles.PUT("", h.Upsert)
	}
	// 评测矩阵工作台（阶段3）：与档案管理同权限门槛。evaluation 缺库时
	// Matrix 为 nil → 不挂载（端点 404，而非空报告/静默降级）。
	if c.Mechanism.Matrix != nil {
		matrix := r.Group("/mechanism/matrix",
			protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"), middleware.RequireDefaultTenant())...)
		matrix.Use(requireActive)
		{
			matrix.GET("", h.MatrixReport)
			matrix.POST("/runs", h.RunMatrix)
			matrix.POST("/adopt", h.AdoptProfile)
		}
	}
}

func registerPrompt(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.Prompt == nil || c.Prompt.Registry == nil {
		return
	}
	h := handler.NewPromptHandler(c.Prompt.Registry, c.Prompt.AB, c.Logger)
	adminMW := middleware.RequireTenantRole("admin")

	prompts := r.Group("/prompts", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	prompts.POST("", adminMW, requireActive, h.CreatePrompt)
	prompts.GET("", adminMW, h.ListPrompts)
	prompts.GET("/:key/versions", h.ListVersions)
	prompts.POST("/:key/versions/:version/publish", adminMW, requireActive, h.PublishVersion)

	bindings := r.Group("/prompts/bindings", protectedTenantMiddleware(c, middleware.RequireTenantRole("admin"))...)
	bindings.GET("", requireActive, h.ListBindings)
	bindings.PUT("", requireActive, h.UpsertBinding)
	bindings.DELETE("/:key/:scope", requireActive, h.DeleteBinding)
}

func registerLLMAdmin(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
	if c.LLMGateway == nil || c.LLMGateway.ProviderService == nil || c.LLMGateway.ModelMgmtService == nil {
		return
	}
	providerH := handler.NewProviderHandler(c.LLMGateway.ProviderService)
	modelMgmtH := handler.NewModelMgmtHandler(c.LLMGateway.ModelMgmtService)
	adminMW := middleware.RequireTenantRole("admin")

	// Providers: list is readable by any tenant member; write ops require admin.
	providers := r.Group("/admin/providers", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		providers.GET("", providerH.List)
		providers.POST("", adminMW, requireActive, providerH.Create)
		providers.PUT("/:id", adminMW, requireActive, providerH.Update)
		providers.DELETE("/:id", adminMW, requireActive, providerH.Delete)
		providers.POST("/:id/discover", adminMW, requireActive, providerH.Discover)
		providers.POST("/:id/health", adminMW, requireActive, providerH.HealthCheck)
	}

	// Models: list and get are readable by any tenant member; write ops require admin.
	models := r.Group("/admin/models", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		models.GET("", modelMgmtH.List)
		models.GET("/:id", modelMgmtH.Get)
		models.PUT("/:id", adminMW, modelMgmtH.Update)
		models.PUT("/:id/default-embedding", adminMW, requireActive, modelMgmtH.SetDefaultEmbedding)
		models.PATCH("/:id/toggle", adminMW, modelMgmtH.Toggle)
		models.DELETE("/:id", adminMW, modelMgmtH.Delete)
	}
}
