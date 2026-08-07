package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type settingsAgentRepo struct {
	cfg       *domain.AgentConfig
	updateErr error
}

func (r *settingsAgentRepo) Register(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *settingsAgentRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	return r.cfg, r.cfg != nil, nil
}
func (r *settingsAgentRepo) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	return r.cfg, r.cfg != nil, nil
}
func (r *settingsAgentRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) { return nil, nil }
func (r *settingsAgentRepo) Remove(_ context.Context, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *settingsAgentRepo) Update(_ context.Context, _ *domain.AgentConfig, _ *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *settingsAgentRepo) UpdateSystemAssistant(_ context.Context, cfg *domain.AgentConfig) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.cfg.Name = cfg.Name
	r.cfg.Description = cfg.Description
	r.cfg.SystemPrompt = cfg.SystemPrompt
	r.cfg.LLMModel = cfg.LLMModel
	r.cfg.MaxIterations = cfg.MaxIterations
	r.cfg.MaxContextTokens = cfg.MaxContextTokens
	r.cfg.MemoryScope = cfg.MemoryScope
	r.cfg.MCPToolIDs = cfg.MCPToolIDs
	r.cfg.KnowledgeWorkspaceIDs = cfg.KnowledgeWorkspaceIDs
	r.cfg.AllowedSkills = cfg.AllowedSkills
	return nil
}

func (r *settingsAgentRepo) UpdateSystemAssistantModel(_ context.Context, model string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	r.cfg.LLMModel = model
	return r.cfg, nil
}
func (r *settingsAgentRepo) UpdateSystemAssistantAll(_ context.Context, _ string, _ string, _ bool, _ int, _ int, _ *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	return r.cfg, nil
}

func (r *settingsAgentRepo) UpdateSystemAssistantBindings(_ context.Context, mcpToolIDs, knowledgeWorkspaceIDs, allowedSkills []string) (*domain.AgentConfig, error) {
	if r.updateErr != nil {
		return nil, r.updateErr
	}
	r.cfg.MCPToolIDs = mcpToolIDs
	r.cfg.KnowledgeWorkspaceIDs = knowledgeWorkspaceIDs
	r.cfg.AllowedSkills = allowedSkills
	return r.cfg, nil
}

type settingsModelValidator struct{ err error }

func (v settingsModelValidator) ValidateTenantChatModel(context.Context, string, string) error {
	return v.err
}

type settingsModelCatalog struct {
	models []string
	err    error
}

func (c settingsModelCatalog) ListTenantChatModels(context.Context, string) ([]string, error) {
	return c.models, c.err
}

var _ port.AgentRepo = (*settingsAgentRepo)(nil)

func newSettingsRouter(repo *settingsAgentRepo, validator port.TenantChatModelValidator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	registry := agentapp.NewRegistry(repo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry: registry, TenantModelValidator: validator,
		TenantRoleResolver: fixedTenantRole{role: "owner"},
		TenantModelCatalog: settingsModelCatalog{models: []string{"qwen-plus"}}, Logger: zap.NewNop(),
	})
	h := NewAgentHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Set(middleware.ContextKeySub, "user-1")
		c.Next()
	})
	r.GET("/agents/system/settings", h.GetSettings)
	r.PUT("/agents/system/settings", h.UpdateModel)
	r.PUT("/agents/:id", h.UpdateAgent)
	r.DELETE("/agents/:id", h.DeleteAgent)
	return r
}

func TestSystemAssistantHandlerMemberGetsSettingsWithoutSecrets(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey, LLMModel: "qwen-plus",
	}}
	r := newSettingsRouter(repo, settingsModelValidator{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agents/system/settings", nil))

	if rec.Code != http.StatusOK || rec.Body.String() !=
		`{"agentId":"stratum-platform-assistant","llmModel":"qwen-plus","ready":true,"availableModels":["qwen-plus"]}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedAgentDTOExposesPublicManagementFieldsWithSystemPrompt(t *testing.T) {
	response := dtoToResponse(agentapp.AgentDTO{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		SystemPrompt: "platform prompt is now visible for editing",
		IsSystem:     true, ManagementMode: "platform",
	})
	if !response.IsSystem || response.ManagementMode != "platform" {
		t.Fatalf("managed fields = isSystem:%v managementMode:%q", response.IsSystem, response.ManagementMode)
	}
	if response.SystemPrompt != "platform prompt is now visible for editing" {
		t.Fatalf("managed system prompt = %q", response.SystemPrompt)
	}
}

func TestManagedAgentDTOExposesSystemPromptRegardlessOfSystemKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        string
		systemKey string
	}{
		{name: "managed id", id: domain.SystemAssistantID},
		{name: "managed key", id: "tenant-copy", systemKey: domain.SystemAssistantKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := dtoToResponse(agentapp.AgentDTO{
				ID: tc.id, SystemKey: tc.systemKey, SystemPrompt: "platform prompt",
			})
			if response.SystemPrompt != "platform prompt" {
				t.Fatalf("system prompt not exposed for %s: %q", tc.name, response.SystemPrompt)
			}
		})
	}
}

func TestCustomAgentDTOPreservesSystemPrompt(t *testing.T) {
	response := dtoToResponse(agentapp.AgentDTO{
		ID: "custom-agent", SystemKey: "", SystemPrompt: "custom prompt",
	})
	if response.SystemPrompt != "custom prompt" {
		t.Fatalf("custom system prompt = %q", response.SystemPrompt)
	}
}

func TestSystemAssistantHandlerUpdateAcceptsOnlyLLMModel(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}}
	r := newSettingsRouter(repo, settingsModelValidator{})
	for _, body := range []string{
		`{"llmModel":"qwen-plus","provider":"qwen"}`,
		`{"llmModel":"qwen-plus","credential":"secret"}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/agents/system/settings", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || rec.Body.String() == "" {
			t.Fatalf("body %s response = %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestSystemAssistantHandlerInvalidModelUsesFrozenErrorBody(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}}
	r := newSettingsRouter(repo, settingsModelValidator{err: domain.ErrInvalidSystemAssistantModel})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/agents/system/settings",
		bytes.NewBufferString(`{"llmModel":"unknown"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"invalid system assistant model"}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestSystemAssistantHandlerPersistenceFailurePropagates(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}, updateErr: errors.New("write failed")}
	r := newSettingsRouter(repo, settingsModelValidator{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/agents/system/settings",
		bytes.NewBufferString(`{"llmModel":"qwen-plus"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"error":"internal server error"}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedAssistantGeneralUpdateSucceeds(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}}
	r := newSettingsRouter(repo, settingsModelValidator{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/agents/"+domain.SystemAssistantID,
		bytes.NewBufferString(`{"name":"ignored","llmModel":"qwen-plus","allowedSkills":[],"mcpToolIds":[],"knowledgeWorkspaceIds":[]}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT system assistant response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedAssistantGeneralDeleteReturnsConflict(t *testing.T) {
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}}
	r := newSettingsRouter(repo, settingsModelValidator{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/agents/"+domain.SystemAssistantID, nil))

	if rec.Code != http.StatusConflict ||
		rec.Body.String() != `{"error":"system assistant is platform managed"}` {
		t.Fatalf("DELETE response = %d %s", rec.Code, rec.Body.String())
	}
}
