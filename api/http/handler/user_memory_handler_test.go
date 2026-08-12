package handler

import (
	"context"
	"encoding/json"
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
	clearErr  error
	listReq   *application.ListUserMemoriesRequest
	listMem   []*application.UserMemory
	listTotal int

	statsMemoryCount int
	statsEntityCount int
	statsErr         error

	entitiesReq   *application.ListUserEntitiesRequest
	entities      []*application.UserMemoryEntity
	entitiesTotal int
	entitiesErr   error
}

func (f *fakeUserMemorySvc) ClearUserMemories(_ context.Context, _ *application.ClearUserMemoriesRequest) error {
	return f.clearErr
}

func (f *fakeUserMemorySvc) ListUserMemories(_ context.Context, req *application.ListUserMemoriesRequest) ([]*application.UserMemory, int, error) {
	f.listReq = req
	return f.listMem, f.listTotal, nil
}

func (f *fakeUserMemorySvc) UserStats(_ context.Context, _, _ string) (int, int, error) {
	return f.statsMemoryCount, f.statsEntityCount, f.statsErr
}

func (f *fakeUserMemorySvc) ListUserEntities(_ context.Context, req *application.ListUserEntitiesRequest) ([]*application.UserMemoryEntity, int, error) {
	f.entitiesReq = req
	return f.entities, f.entitiesTotal, f.entitiesErr
}

type fakeMemoryMgr struct{}

func (f *fakeMemoryMgr) Clear(_ context.Context, _ *application.SessionContext) error { return nil }
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
			h := NewUserMemoryHandler(&fakeUserMemorySvc{}, &fakeMemoryMgr{}, tc.resolver)
			r := gin.New()
			r.Use(middleware.ErrorHandler(zap.NewNop()))
			r.GET("/api/memory/stats", func(c *gin.Context) {
				ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
				c.Request = c.Request.WithContext(ctx)
				c.Set(middleware.ContextKeySub, "user-1")
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

func setupMemoryStatsRouter(svc *fakeUserMemorySvc, tenantID, userID string, handler func(*gin.Context)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory/stats", func(c *gin.Context) {
		if tenantID != "" {
			ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
			c.Request = c.Request.WithContext(ctx)
		}
		if userID != "" {
			c.Set(middleware.ContextKeySub, userID)
		}
		handler(c)
	})
	return r
}

func TestGetStats_returnsUserLevelCounts(t *testing.T) {
	svc := &fakeUserMemorySvc{statsMemoryCount: 3, statsEntityCount: 5}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := setupMemoryStatsRouter(svc, "tenant-1", "user-1", h.GetStats)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		MemoryCount int64 `json:"memory_count"`
		EntityCount int64 `json:"entity_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MemoryCount != 3 || got.EntityCount != 5 {
		t.Fatalf("memory_count=%d entity_count=%d, want 3/5", got.MemoryCount, got.EntityCount)
	}
}

func TestGetStats_missingIdentity(t *testing.T) {
	h := NewUserMemoryHandler(&fakeUserMemorySvc{}, nil, nil)
	r := setupMemoryStatsRouter(&fakeUserMemorySvc{}, "", "", h.GetStats)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetStats_missingTenant(t *testing.T) {
	h := NewUserMemoryHandler(&fakeUserMemorySvc{}, nil, nil)
	r := setupMemoryStatsRouter(&fakeUserMemorySvc{}, "", "user-1", h.GetStats)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetStats_serviceError(t *testing.T) {
	svc := &fakeUserMemorySvc{statsErr: errors.New("db error")}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := setupMemoryStatsRouter(svc, "tenant-1", "user-1", h.GetStats)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/stats", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func setupMemoryEntitiesRouter(svc *fakeUserMemorySvc, tenantID, userID string, handler func(*gin.Context)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/api/memory/entities", func(c *gin.Context) {
		if tenantID != "" {
			ctx := reqctx.WithTenantID(c.Request.Context(), tenantID)
			c.Request = c.Request.WithContext(ctx)
		}
		if userID != "" {
			c.Set(middleware.ContextKeySub, userID)
		}
		handler(c)
	})
	return r
}

func TestGetEntities_paginationUsesAuthenticatedIdentity(t *testing.T) {
	svc := &fakeUserMemorySvc{
		entities: []*application.UserMemoryEntity{
			{ID: "ent-2", Name: "Python", EntityType: "tech", FactCount: 4},
			{ID: "ent-1", Name: "Alice", EntityType: "person", FactCount: 2},
		},
		entitiesTotal: 2,
	}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := setupMemoryEntitiesRouter(svc, "tenant-1", "user-1", h.GetEntities)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/entities?page=2&page_size=10", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.entitiesReq.TenantID != "tenant-1" || svc.entitiesReq.UserID != "user-1" {
		t.Fatalf("handler used wrong identity: %#v", svc.entitiesReq)
	}
	if svc.entitiesReq.Limit != 10 || svc.entitiesReq.Offset != 10 {
		t.Fatalf("limit=%d offset=%d, want 10/10", svc.entitiesReq.Limit, svc.entitiesReq.Offset)
	}
	var got struct {
		Entities []map[string]any `json:"entities"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Entities) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", got.Total, len(got.Entities))
	}
	if got.Entities[0]["name"] != "Python" || got.Entities[0]["fact_count"] != float64(4) {
		t.Fatalf("unexpected first entity: %#v", got.Entities[0])
	}
}

func TestGetEntities_missingIdentity(t *testing.T) {
	h := NewUserMemoryHandler(&fakeUserMemorySvc{}, nil, nil)
	r := setupMemoryEntitiesRouter(&fakeUserMemorySvc{}, "", "", h.GetEntities)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/entities", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetEntities_serviceError(t *testing.T) {
	svc := &fakeUserMemorySvc{entitiesErr: errors.New("db error")}
	h := NewUserMemoryHandler(svc, nil, nil)
	r := setupMemoryEntitiesRouter(svc, "tenant-1", "user-1", h.GetEntities)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/memory/entities", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}
