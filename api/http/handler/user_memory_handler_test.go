package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type fakeUserMemorySvc struct {
	clearErr  error
	created   *application.UserMemory
	createReq *application.CreateUserMemoryRequest
}

func (f *fakeUserMemorySvc) ClearUserMemories(_ context.Context, _ *application.ClearUserMemoriesRequest) error {
	return f.clearErr
}

func (f *fakeUserMemorySvc) CreateUserMemory(_ context.Context, req *application.CreateUserMemoryRequest) (*application.UserMemory, error) {
	f.createReq = req
	return f.created, nil
}

func (f *fakeUserMemorySvc) GetUserMemory(_ context.Context, _ *application.GetUserMemoryRequest) (*application.UserMemory, error) {
	return nil, domain.ErrFactNotFound
}

func (f *fakeUserMemorySvc) ForgetUserMemory(_ context.Context, _ *application.ForgetMemoryRequest) error {
	return nil
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

	h := NewUserMemoryHandler(svc, nil)
	r.DELETE("/api/memory/clear", injectClaims, h.ClearMemories)
	return r
}

func TestAddMemory_UsesAuthenticatedIdentityAndCanonicalDTO(t *testing.T) {
	svc := &fakeUserMemorySvc{created: &application.UserMemory{ID: "fact-1", Scope: "user", Content: "likes Go", Importance: 0.7}}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	h := NewUserMemoryHandler(svc, nil)
	r.POST("/api/memory", func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
		c.Request = c.Request.WithContext(ctx)
		c.Set(middleware.ContextKeySub, "user-1")
	}, h.AddMemory)

	body, _ := json.Marshal(map[string]any{
		"content": "likes Go", "importance": 0.7,
		"tenant_id": "attacker", "user_id": "attacker", "agent_id": "foreign-agent",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/memory", bytes.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if svc.createReq.TenantID != "tenant-1" || svc.createReq.UserID != "user-1" {
		t.Fatalf("handler trusted body identity: %#v", svc.createReq)
	}
	if svc.createReq.AgentID != "" || svc.createReq.ConversationID != "" {
		t.Fatalf("user endpoint accepted unverified ownership references: %#v", svc.createReq)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "fact-1" || got["scope"] != "user" || got["content"] != "likes Go" {
		t.Fatalf("unexpected response: %#v", got)
	}
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
