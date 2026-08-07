package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/prompt/application"
	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/byteBuilderX/stratum/internal/prompt/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakePromptRepo simulates the prompt storage for handler-level tests.
type fakePromptRepo struct {
	err error
}

func (f *fakePromptRepo) Insert(context.Context, domain.PromptTemplate) error { return f.err }
func (f *fakePromptRepo) GetByKey(context.Context, string, *string) ([]domain.PromptTemplate, error) {
	return nil, f.err
}
func (f *fakePromptRepo) GetVersion(context.Context, string, int, *string) (*domain.PromptTemplate, error) {
	return nil, f.err
}
func (f *fakePromptRepo) GetLatestPublished(context.Context, string, *string) (*domain.PromptTemplate, error) {
	return nil, f.err
}
func (f *fakePromptRepo) UpdateStatus(context.Context, string, int, *string, domain.PromptStatus) error {
	return f.err
}
func (f *fakePromptRepo) GetByHash(context.Context, string) (*domain.PromptTemplate, error) {
	return nil, f.err
}

// fakeBindingRepo simulates the binding storage for handler-level tests.
type fakeBindingRepo struct {
	err error
}

func (f *fakeBindingRepo) UpsertBinding(context.Context, domain.PromptBinding) error { return f.err }
func (f *fakeBindingRepo) GetBinding(context.Context, string, string) (*domain.PromptBinding, error) {
	return nil, f.err
}
func (f *fakeBindingRepo) ListBindings(context.Context, string) ([]domain.PromptBinding, error) {
	return nil, f.err
}
func (f *fakeBindingRepo) DeleteBinding(context.Context, string, string) error { return f.err }

// newPromptTestRouter wires the prompt handler behind the unified ErrorHandler
// middleware, mirroring production wiring in api/http/router.go.
func newPromptTestRouter(t *testing.T, prompts port.PromptRepo, bindings port.BindingRepo) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	registry := application.NewRegistryService(prompts, bindings)
	ab := application.NewABService(bindings, prompts)
	h := NewPromptHandler(registry, ab, zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	group := r.Group("/v1/prompts", withPromptIdentity("tenant-1", "user-1"))
	group.POST("", h.CreatePrompt)
	group.GET("/:key/versions", h.ListVersions)
	group.POST("/:key/versions/:version/publish", h.PublishVersion)
	group.PUT("/bindings", h.UpsertBinding)
	group.DELETE("/bindings/:key/:scope", h.DeleteBinding)
	return r
}

// withPromptIdentity sets the JWT-derived context keys the prompt handler reads.
func withPromptIdentity(tenantID, userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.ContextKeyTenantID, tenantID)
		c.Set(middleware.ContextKeySub, userID)
		c.Next()
	}
}

func TestPromptHandlerErrorsGoThroughUnifiedMiddleware(t *testing.T) {
	seededRepoErr := errors.New("internal secret detail")

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "create bind error maps to 400",
			method:     http.MethodPost,
			path:       "/v1/prompts",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `"error"`,
		},
		{
			name:       "create repo error is a sanitized 500",
			method:     http.MethodPost,
			path:       "/v1/prompts",
			body:       `{"key":"k","content":"c"}`,
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal server error"}`,
		},
		{
			name:       "list versions repo error is a sanitized 500",
			method:     http.MethodGet,
			path:       "/v1/prompts/k/versions",
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal server error"}`,
		},
		{
			name:       "publish invalid version maps to 400",
			method:     http.MethodPost,
			path:       "/v1/prompts/k/versions/not-a-number/publish",
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid version"}`,
		},
		{
			name:       "publish repo error is a sanitized 500",
			method:     http.MethodPost,
			path:       "/v1/prompts/k/versions/1/publish",
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal server error"}`,
		},
		{
			name:       "upsert binding bind error maps to 400",
			method:     http.MethodPut,
			path:       "/v1/prompts/bindings",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `"error"`,
		},
		{
			name:       "delete binding repo error is a sanitized 500",
			method:     http.MethodDelete,
			path:       "/v1/prompts/bindings/k/tenant:tenant-1",
			wantStatus: http.StatusInternalServerError,
			wantBody:   `{"error":"internal server error"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakePromptRepo{err: seededRepoErr}
			bindings := &fakeBindingRepo{}
			if tt.method == http.MethodDelete {
				bindings.err = seededRepoErr
			}
			r := newPromptTestRouter(t, repo, bindings)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBody)
			}
			if tt.wantStatus >= http.StatusInternalServerError &&
				strings.Contains(rec.Body.String(), "secret") {
				t.Fatalf("response leaks internal error text: %s", rec.Body.String())
			}
		})
	}
}

func TestPromptHandlerCreateSucceeds(t *testing.T) {
	r := newPromptTestRouter(t, &fakePromptRepo{}, &fakeBindingRepo{})
	req := httptest.NewRequest(http.MethodPost, "/v1/prompts", strings.NewReader(`{"key":"k","content":"c"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"key":"k"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
