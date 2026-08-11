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
	listReq   *application.ListUserMemoriesRequest
	listMem   []*application.UserMemory
	listTotal int
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

func (f *fakeUserMemorySvc) ListUserMemories(_ context.Context, req *application.ListUserMemoriesRequest) ([]*application.UserMemory, int, error) {
	f.listReq = req
	return f.listMem, f.listTotal, nil
}

type fakeMemoryMgr struct {
	stats *application.MemoryStats
	err   error
}

func (f *fakeMemoryMgr) Add(_ context.Context, _ *application.MemoryEntry) error { return nil }
func (f *fakeMemoryMgr) Get(_ context.Context, _ string) (*application.MemoryEntry, error) {
	return nil, nil
}
func (f *fakeMemoryMgr) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeMemoryMgr) Clear(_ context.Context, _ *application.SessionContext) error {
	return nil
}
func (f *fakeMemoryMgr) GetStats(_ context.Context, _ *application.SessionContext) (*application.MemoryStats, error) {
	return f.stats, f.err
}
func (f *fakeMemoryMgr) GetSummary(_ context.Context, _ *application.SessionContext) (string, error) {
	return "", nil
}

type fakeEmbedResolver struct {
	model string
	err   error
}

func (f *fakeEmbedResolver) ResolveDefaultEmbeddingModel(_ context.Context, _ string) (string, error) {
	return f.model, f.err
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

	h := NewUserMemoryHandler(svc, nil, nil)
	r.DELETE("/api/memory/clear", injectClaims, h.ClearMemories)
	return r
}

func TestAddMemory_UsesAuthenticatedIdentityAndCanonicalDTO(t *testing.T) {
	svc := &fakeUserMemorySvc{created: &application.UserMemory{ID: "fact-1", Scope: "user", Content: "likes Go", Importance: 0.7}}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	h := NewUserMemoryHandler(svc, nil, nil)
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

func TestListMemories_paginationUsesAuthenticatedIdentity(t *testing.T) {
	svc := &fakeUserMemorySvc{
		listMem: []*application.UserMemory{
			{ID: "fact-2", Scope: "user", Content: "prefers Go", Importance: 0.8},
			{ID: "fact-1", Scope: "user", Content: "likes Go", Importance: 0.7},
		},
		listTotal: 2,
	}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory", func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
		c.Request = c.Request.WithContext(ctx)
		c.Set(middleware.ContextKeySub, "user-1")
	}, h.ListMemories)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory?page=2&page_size=10", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.listReq.TenantID != "tenant-1" || svc.listReq.UserID != "user-1" {
		t.Fatalf("handler used wrong identity: %#v", svc.listReq)
	}
	if svc.listReq.Limit != 10 || svc.listReq.Offset != 10 {
		t.Fatalf("limit=%d offset=%d, want 10/10", svc.listReq.Limit, svc.listReq.Offset)
	}
	var got struct {
		Memories []map[string]any `json:"memories"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Memories) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", got.Total, len(got.Memories))
	}
	if got.Memories[0]["id"] != "fact-2" {
		t.Fatalf("unexpected first memory: %#v", got.Memories[0])
	}
}

func TestListMemories_clampsInvalidPagination(t *testing.T) {
	svc := &fakeUserMemorySvc{}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory", func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
		c.Request = c.Request.WithContext(ctx)
		c.Set(middleware.ContextKeySub, "user-1")
	}, h.ListMemories)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory?page=0&page_size=-5", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.listReq.Limit != 20 || svc.listReq.Offset != 0 {
		t.Fatalf("limit=%d offset=%d, want clamped 20/0", svc.listReq.Limit, svc.listReq.Offset)
	}
}

func TestGetStats_embedModelConfigured(t *testing.T) {
	cases := []struct {
		name     string
		resolver DefaultEmbedModelResolver
		want     bool
	}{
		{name: "resolver nil → false", resolver: nil, want: false},
		{name: "resolver error → false", resolver: &fakeEmbedResolver{err: errors.New("registry down")}, want: false},
		{name: "no embedding model → false", resolver: &fakeEmbedResolver{model: ""}, want: false},
		{name: "embedding model resolved → true", resolver: &fakeEmbedResolver{model: "text-embedding-3-small"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewUserMemoryHandler(&fakeUserMemorySvc{}, &fakeMemoryMgr{
				stats: &application.MemoryStats{TotalEntries: 3},
			}, tc.resolver)
			r := gin.New()
			r.Use(middleware.ErrorHandler(zap.NewNop()))
			r.GET("/api/memory/stats", func(c *gin.Context) {
				ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
				c.Request = c.Request.WithContext(ctx)
			}, h.GetStats)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got["embed_model_configured"] != tc.want {
				t.Fatalf("embed_model_configured=%v, want %v (body %s)", got["embed_model_configured"], tc.want, w.Body.String())
			}
		})
	}
}

func TestGetStats_missingTenant(t *testing.T) {
	h := NewUserMemoryHandler(&fakeUserMemorySvc{}, &fakeMemoryMgr{}, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory/stats", h.GetStats)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestListMemories_missingIdentity(t *testing.T) {
	h := NewUserMemoryHandler(&fakeUserMemorySvc{}, nil, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory", h.ListMemories)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
