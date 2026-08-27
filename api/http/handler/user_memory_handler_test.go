package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

	factsReq          *application.ListUserFactsFilteredRequest
	factDetails       []*application.UserFactDetail
	factTotal         int
	factsErr          error
	factDetail        *application.UserFactDetail
	factErr           error
	vectorSyncFailed  bool
	summaries         []*application.UserSummary
	summaryTotal      int
	snapshots         []*application.UserSnapshot
	snapshot          *application.UserSnapshot
	lastSnapshotPatch *application.UpdateUserSnapshotPatch
	entries           []*application.UserEntry
	entryTotal        int
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

func (f *fakeUserMemorySvc) ListUserFactsFiltered(_ context.Context, req *application.ListUserFactsFilteredRequest) ([]*application.UserFactDetail, int, error) {
	f.factsReq = req
	return f.factDetails, f.factTotal, f.factsErr
}

func (f *fakeUserMemorySvc) GetUserFact(_ context.Context, _, _, _ string) (*application.UserFactDetail, error) {
	return f.factDetail, f.factErr
}

func (f *fakeUserMemorySvc) UpdateUserFact(_ context.Context, _, _, _ string, _ *application.UpdateUserFactPatch) (*application.UserFactDetail, bool, error) {
	return f.factDetail, f.vectorSyncFailed, f.factErr
}

func (f *fakeUserMemorySvc) DeleteUserFact(_ context.Context, _, _, _ string) error { return f.factErr }

func (f *fakeUserMemorySvc) DeleteUserEntity(_ context.Context, _, _, _ string) error {
	return f.factErr
}

func (f *fakeUserMemorySvc) ListUserSummaries(_ context.Context, _, _ string, _, _ int) ([]*application.UserSummary, int, error) {
	return f.summaries, f.summaryTotal, f.factErr
}

func (f *fakeUserMemorySvc) DeleteUserSummary(_ context.Context, _, _, _ string) error {
	return f.factErr
}

func (f *fakeUserMemorySvc) ListUserSnapshots(_ context.Context, _, _ string) ([]*application.UserSnapshot, error) {
	return f.snapshots, f.factErr
}

func (f *fakeUserMemorySvc) UpdateUserSnapshot(_ context.Context, _, _, _ string, patch *application.UpdateUserSnapshotPatch) (*application.UserSnapshot, error) {
	f.lastSnapshotPatch = patch
	return f.snapshot, f.factErr
}

func (f *fakeUserMemorySvc) DeleteUserSnapshot(_ context.Context, _, _, _ string) error {
	return f.factErr
}

func (f *fakeUserMemorySvc) ListUserEntries(_ context.Context, _, _ string, _, _ int, _ string) ([]*application.UserEntry, int, error) {
	return f.entries, f.entryTotal, f.factErr
}

func (f *fakeUserMemorySvc) DeleteUserEntry(_ context.Context, _, _, _ string) error {
	return f.factErr
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

func (f *fakeEmbedResolver) ResolveMemoryEmbeddingModel(_ context.Context, _ string) (string, error) {
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
	g := r.Group("/memory", injectClaims)
	g.DELETE("/clear", h.ClearMemories)
	g.GET("", h.ListMemories)
	g.POST("/sessions", h.ListSessions)
	g.GET("/stats", h.GetStats)
	g.GET("/entities", h.GetEntities)
	g.GET("/summary/:session_id", h.GetSummary)
	g.DELETE("/session/:session_id", h.ClearSession)
	g.GET("/facts", h.ListFacts)
	g.GET("/facts/:id", h.GetFact)
	g.PATCH("/facts/:id", h.UpdateFact)
	g.DELETE("/facts/:id", h.DeleteFact)
	g.DELETE("/entities/:id", h.DeleteEntity)
	g.GET("/summaries", h.ListSummaries)
	g.DELETE("/summaries/:id", h.DeleteSummary)
	g.GET("/snapshots", h.ListSnapshots)
	g.PATCH("/snapshots/:agent_id", h.UpdateSnapshot)
	g.DELETE("/snapshots/:agent_id", h.DeleteSnapshot)
	g.GET("/entries", h.ListEntries)
	g.DELETE("/entries/:id", h.DeleteEntry)

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
		resolver MemoryEmbeddingModelResolver
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

func TestUserMemoryHandler_ListFacts(t *testing.T) {
	svc := &fakeUserMemorySvc{factDetails: []*application.UserFactDetail{
		{ID: "fact-1", Content: "dark mode", Category: "preference", Confidence: 0.9},
	}, factTotal: 1}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	req := httptest.NewRequest(http.MethodGet, "/memory/facts?q=dark&category=preference&page=1&page_size=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"category":"preference"`)
}

func TestUserMemoryHandler_UpdateFact(t *testing.T) {
	svc := &fakeUserMemorySvc{factDetail: &application.UserFactDetail{ID: "fact-1", Content: "new content"}}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	body := `{"content":"new content"}`
	req := httptest.NewRequest(http.MethodPatch, "/memory/facts/fact-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"content":"new content"`)
	require.Contains(t, w.Body.String(), `"vector_sync_failed":false`)
}

func TestUserMemoryHandler_UpdateFactEmptyPatch(t *testing.T) {
	svc := &fakeUserMemorySvc{}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	req := httptest.NewRequest(http.MethodPatch, "/memory/facts/fact-1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUserMemoryHandler_DeleteFact(t *testing.T) {
	svc := &fakeUserMemorySvc{}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	req := httptest.NewRequest(http.MethodDelete, "/memory/facts/fact-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
}

// ListEntries 的 gen.MemoryEntryItemResponse.ExpiresAt 是非指针 time.Time：
// nil 过期时间落零值，非 nil 原样透传（防 panic / 防泄漏内部指针）。
func TestUserMemoryHandler_ListEntries_ExpiresAtConversion(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	expires := now.Add(24 * time.Hour)
	svc := &fakeUserMemorySvc{
		entries: []*application.UserEntry{
			{ID: "entry-1", Role: "user", Content: "hello", Type: "text", Scope: "user", Importance: 0.5, CreatedAt: now, ExpiresAt: &expires},
			{ID: "entry-2", Role: "assistant", Content: "hi", Type: "text", Scope: "user", Importance: 0.4, CreatedAt: now},
		},
		entryTotal: 2,
	}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	req := httptest.NewRequest(http.MethodGet, "/memory/entries?page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Entries []map[string]any `json:"entries"`
		Total   int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, int64(2), got.Total)
	require.Equal(t, expires.Format(time.RFC3339Nano), got.Entries[0]["expires_at"])
	require.Equal(t, time.Time{}.Format(time.RFC3339Nano), got.Entries[1]["expires_at"])
}

// UpdateSnapshot 是整段替换语义：三段数组必须原样透传给 service，
// 不允许 handler 做「仅当存在才更新」的合并（proto3 无法区分空数组与缺省）。
func TestUserMemoryHandler_UpdateSnapshot_WholeReplace(t *testing.T) {
	svc := &fakeUserMemorySvc{snapshot: &application.UserSnapshot{
		AgentID: "agent-1", WorkContext: []string{"w1"}, PersonalContext: []string{"p1"},
		TopOfMind: []string{"t1"}, Status: "active",
	}}
	r := setupUserMemoryRouter(svc, "tenant-1", "user-1")
	body := `{"work_context":["ctx-a","ctx-b"],"personal_context":["personal-x"],"top_of_mind":["mind-1"]}`
	req := httptest.NewRequest(http.MethodPatch, "/memory/snapshots/agent-1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.lastSnapshotPatch)
	require.Equal(t, []string{"ctx-a", "ctx-b"}, svc.lastSnapshotPatch.WorkContext)
	require.Equal(t, []string{"personal-x"}, svc.lastSnapshotPatch.PersonalContext)
	require.Equal(t, []string{"mind-1"}, svc.lastSnapshotPatch.TopOfMind)
}
