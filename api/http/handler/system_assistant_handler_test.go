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
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type settingsAgentRepo struct {
	cfg       *domain.AgentConfig
	updateErr error
}

func (r *settingsAgentRepo) Register(context.Context, *domain.AgentConfig) error { return nil }
func (r *settingsAgentRepo) Get(context.Context, string) (*domain.AgentConfig, bool, error) {
	return r.cfg, r.cfg != nil, nil
}
func (r *settingsAgentRepo) GetSystemAssistant(context.Context) (*domain.AgentConfig, bool, error) {
	return r.cfg, r.cfg != nil, nil
}
func (r *settingsAgentRepo) GetAll(context.Context) ([]*domain.AgentConfig, error) { return nil, nil }
func (r *settingsAgentRepo) Remove(context.Context, string) error                  { return nil }
func (r *settingsAgentRepo) Update(context.Context, *domain.AgentConfig) error     { return nil }
func (r *settingsAgentRepo) UpdateSystemAssistantModel(_ context.Context, model string) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.cfg.LLMModel = model
	return nil
}

type settingsModelValidator struct{ err error }

func (v settingsModelValidator) ValidateTenantChatModel(context.Context, string, string) error {
	return v.err
}

var _ port.AgentRepo = (*settingsAgentRepo)(nil)

func newSettingsRouter(repo *settingsAgentRepo, validator port.TenantChatModelValidator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	registry := agentapp.NewRegistry(repo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
		Registry: registry, TenantModelValidator: validator, Logger: zap.NewNop(),
	})
	h := NewAgentHandler(svc, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.GET("/agents/system/settings", h.GetSettings)
	r.PUT("/agents/system/settings", h.UpdateModel)
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
		`{"agentId":"stratum-platform-assistant","llmModel":"qwen-plus","ready":true}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestManagedAgentDTOExposesPublicManagementFieldsWithoutSystemKey(t *testing.T) {
	response := dtoToResponse(agentapp.AgentDTO{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
		IsSystem: true, ManagementMode: "platform",
	})
	if !response.IsSystem || response.ManagementMode != "platform" {
		t.Fatalf("managed fields = isSystem:%v managementMode:%q", response.IsSystem, response.ManagementMode)
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
