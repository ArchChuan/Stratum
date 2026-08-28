package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// stubCreateModelRepo 实现 ModelRepository + PlatformModelRepository 的最小面，
// 满足 handler Create 测试所需。
type stubCreateModelRepo struct {
	created *domain.Model
}

func (r *stubCreateModelRepo) Create(context.Context, *domain.Model) error { return nil }
func (r *stubCreateModelRepo) Get(context.Context, string) (*domain.Model, error) {
	return nil, nil
}
func (r *stubCreateModelRepo) List(context.Context, port.ModelFilter) ([]domain.Model, error) {
	return nil, nil
}
func (r *stubCreateModelRepo) Update(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *stubCreateModelRepo) UpsertDiscovered(context.Context, string, []domain.Model) ([]domain.Model, error) {
	return nil, nil
}
func (r *stubCreateModelRepo) Delete(context.Context, string) error { return nil }
func (r *stubCreateModelRepo) Toggle(context.Context, string, bool) error {
	return nil
}
func (r *stubCreateModelRepo) UpdatePlatform(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *stubCreateModelRepo) CreatePlatform(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	r.created = m
	return nil
}

// stubCreateProviderRepo 让 Create 路径的 provider 校验通过。
type stubCreateProviderRepo struct{}

func (s *stubCreateProviderRepo) Create(context.Context, *domain.Provider) error { return nil }
func (s *stubCreateProviderRepo) Get(context.Context, string) (*domain.Provider, error) {
	return &domain.Provider{ID: "p-1"}, nil
}
func (s *stubCreateProviderRepo) GetMeta(context.Context, string) (*domain.Provider, error) {
	return nil, nil
}
func (s *stubCreateProviderRepo) List(context.Context) ([]domain.Provider, error) { return nil, nil }
func (s *stubCreateProviderRepo) Update(context.Context, *domain.Provider, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (s *stubCreateProviderRepo) Delete(context.Context, string) error { return nil }

func TestModelMgmtHandlerCreate(t *testing.T) {
	repo := &stubCreateModelRepo{}
	h := NewModelMgmtHandler(llmapp.NewModelMgmtService(repo).WithProviderRepo(&stubCreateProviderRepo{}))
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySub, "actor-1")
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.POST("/admin/models", h.Create)

	req := httptest.NewRequest(http.MethodPost, "/admin/models",
		strings.NewReader(`{"providerId":"p-1","name":"gpt-x","capabilities":["chat"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "gpt-x", resp["name"])
	require.Equal(t, false, resp["providerManaged"])
	require.Equal(t, "p-1", resp["providerId"])
	require.NotNil(t, repo.created, "repo must receive the created model")
}
