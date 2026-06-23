package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeUserMemorySvc struct {
	clearErr error
}

func (f *fakeUserMemorySvc) ClearUserMemories(_ context.Context, _ *application.ClearUserMemoriesRequest) error {
	return f.clearErr
}

func setupUserMemoryRouter(svc *fakeUserMemorySvc, tenantID, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))

	injectClaims := func(c *gin.Context) {
		if tenantID != "" {
			ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
			c.Request = c.Request.WithContext(ctx)
		}
		if userID != "" {
			c.Set(middleware.ContextKeySub, userID)
		}
		c.Next()
	}

	h := NewUserMemoryHandler(svc)
	r.DELETE("/api/memory/clear", injectClaims, h.ClearMemories)
	return r
}

func TestClearMemories_success(t *testing.T) {
	r := setupUserMemoryRouter(&fakeUserMemorySvc{}, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/memory/clear", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestClearMemories_missingTenant(t *testing.T) {
	r := setupUserMemoryRouter(&fakeUserMemorySvc{}, "", "user-1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/memory/clear", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestClearMemories_missingUser(t *testing.T) {
	r := setupUserMemoryRouter(&fakeUserMemorySvc{}, "tenant-1", "")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/memory/clear", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestClearMemories_serviceError(t *testing.T) {
	svc := &fakeUserMemorySvc{clearErr: errors.New("db error")}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/api/memory/clear", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}
