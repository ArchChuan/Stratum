package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// ragModelExistsStub 实现 knowledgeport.ModelExists；目录为空时任何模型都不存在
// （使非法 embedding 模型在 application 层被拒）。CapChat 仅认 qwen-turbo，
// rerank/judge 模型目录校验按模型名判定。
type ragModelExistsStub struct{ embedding map[string]bool }

func (m ragModelExistsStub) Exists(_ context.Context, model string, capability knowledgeport.ModelCapability) (bool, error) {
	switch capability {
	case knowledgeport.CapRerank:
		return false, nil
	case knowledgeport.CapChat:
		return model == "qwen-turbo", nil
	default:
		return m.embedding[model], nil
	}
}

// injectRAGTenant sets a tenant context for RAG handler tests.
func injectRAGTenant(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "user-test", Role: tenantdb.RoleTenantAdmin}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		ctx = reqctx.WithTenantID(ctx, tenantID)
		c.Request = c.Request.WithContext(ctx)
		// Write handlers resolve the actor via ContextKeySub.
		c.Set(middleware.ContextKeySub, "user-test")
		c.Next()
	}
}

// newMinimalRAGHandler constructs a handler suitable for missing-tenant tests
// where the service is never reached.
func newMinimalRAGHandler() *RAGHandler {
	return NewRAGHandler(nil, nil, zap.NewNop())
}

// newValidationRAGHandler constructs a handler whose WorkspaceService is wired
// with a nil repo and an empty catalogue. Structural validation errors
// (query mode / rerank / threshold) come from the domain factory before the
// repo is ever called; embedding model existence is rejected by the
// application catalogue check (empty stub directory).
func newValidationRAGHandler() *RAGHandler {
	ws := knowledge.NewWorkspaceService(nil, nil, zap.NewNop())
	ws.SetTenantRoleResolver(fixedTenantRole{role: "owner"})
	ws.SetModelExists(ragModelExistsStub{embedding: map[string]bool{}})
	return NewRAGHandler(nil, ws, zap.NewNop())
}

// newRouterWithErrorHandler returns a gin engine with the centralised error
// mapping middleware installed so domain sentinels surface as JSON.
func newRouterWithErrorHandler() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	return r
}

func TestListWorkspaces_MissingTenant(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newMinimalRAGHandler()
	r.GET("/knowledge/workspaces", h.ListWorkspaces)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/knowledge/workspaces", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspace_MissingTenant(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/workspaces", h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{"name": "test"})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateWorkspace_InvalidEmbeddingModel(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newValidationRAGHandler()
	r.POST("/knowledge/workspaces", injectRAGTenant("test-tenant-id"), h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{
		"name":   "test",
		"config": map[string]any{"embedding_model": "gpt-4"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "unsupported embedding model" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}

func TestCreateWorkspace_InvalidQueryMode(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newValidationRAGHandler()
	r.POST("/knowledge/workspaces", injectRAGTenant("test-tenant-id"), h.CreateWorkspace)

	body, _ := json.Marshal(map[string]any{
		"name":   "test",
		"config": map[string]any{"query_mode": "invalid"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid query_mode" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}

// TestCreateWorkspace_RejectsOutOfRangeRerankParams 守护 proto binding 对
// top_k / rerank_top_k / score_threshold 的契约层拒绝。handler 用 nil repo
// 的 service——binding 若能穿过并到达 service 会 panic，400 即证明
// validator.v10 在绑定层短路（service 未被调用）。
func TestCreateWorkspace_RejectsOutOfRangeRerankParams(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newValidationRAGHandler()
	r.POST("/knowledge/workspaces", injectRAGTenant("test-tenant-id"), h.CreateWorkspace)

	for _, tc := range []struct {
		name string
		cfg  map[string]any
	}{
		{"top_k above max", map[string]any{"top_k": 21}},
		{"rerank_top_k above max", map[string]any{"rerank_top_k": 21}},
		{"rerank_top_k negative", map[string]any{"rerank_top_k": -1}},
		{"score_threshold above max", map[string]any{"score_threshold": 1.5}},
		{"score_threshold below min", map[string]any{"score_threshold": -0.1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"name": "test", "config": tc.cfg})
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/workspaces", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400 (binding reject), got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestQuery_MissingTenant(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newMinimalRAGHandler()
	r.POST("/knowledge/query", h.Query)

	body, _ := json.Marshal(map[string]any{
		"question": "hello", "workspace": "ws", "mode": "hybrid",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// injectRAGTenantNoUser sets tenant context but no actor identity: the
// query path must fail closed (401) instead of leaking an anonymous viewer.
func injectRAGTenantNoUser(tenantID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "", Role: tenantdb.RoleTenantAdmin}
		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		ctx = reqctx.WithTenantID(ctx, tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func TestQuery_MissingUser(t *testing.T) {
	r := newRouterWithErrorHandler()
	r.Use(injectRAGTenantNoUser("tenant-1"))
	h := newMinimalRAGHandler()
	r.POST("/knowledge/query", h.Query)

	body, _ := json.Marshal(map[string]any{
		"question": "hello", "workspace": "ws", "mode": "hybrid",
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/knowledge/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestDeleteWorkspace_MissingTenant(t *testing.T) {
	r := newRouterWithErrorHandler()
	h := newMinimalRAGHandler()
	r.DELETE("/knowledge/workspaces/:name", h.DeleteWorkspace)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/knowledge/workspaces/myws", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// TestUpdateWorkspaceRejectsBuiltinWithoutRerankModel 守护 Global Constraint 2:
// builtin-score-v1 但 workspace 无显式 rerank_model → PATCH 必须 400
// （ErrRerankModelRequired），不自动降级、不静默兜底。显式空字符串经 sentinel
// 编码后在 domain 合并层清空，application 层目录校验拒绝。
func TestUpdateWorkspaceRejectsBuiltinWithoutRerankModel(t *testing.T) {
	ws, err := domain.NewWorkspace("kb", "desc",
		domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}, domain.DefaultChunkSize, domain.DefaultTopK)
	if err != nil {
		t.Fatal(err)
	}
	ws.ID = "wsid-1"
	svc := knowledge.NewWorkspaceService(&previewWorkspaceRepo{ws: ws}, nil, zap.NewNop())
	svc.SetTenantRoleResolver(fixedTenantRole{role: "owner"})
	svc.SetModelExists(ragModelExistsStub{embedding: map[string]bool{"text-embedding-v3": true}})

	r := newRouterWithErrorHandler()
	r.PATCH("/knowledge/workspaces/:name", injectRAGTenant("tenant-1"), func(c *gin.Context) {
		NewRAGHandler(nil, svc, zap.NewNop()).UpdateWorkspace(c)
	})

	body := `{"config":{"embedding_model":"text-embedding-v3","reranking":"builtin-score-v1","rerank_model":""}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/knowledge/workspaces/kb", strings.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestDTOConfigRoundTripRerankModels 守护 RerankModel/JudgeModel 经
// toDTOConfig/fromDTOConfig 双向映射不丢失。
func TestDTOConfigRoundTripRerankModels(t *testing.T) {
	in := domain.WorkspaceConfig{
		EmbeddingModel: "text-embedding-v3", QueryMode: "hybrid", Reranking: "builtin-score-v1",
		RerankModel: "qwen-turbo", JudgeModel: "qwen-plus",
	}
	got := fromDTOConfig(toDTOConfig(in))
	if got.RerankModel != "qwen-turbo" || got.JudgeModel != "qwen-plus" {
		t.Fatalf("round-trip lost models: RerankModel=%q JudgeModel=%q", got.RerankModel, got.JudgeModel)
	}
}

// TestEncodeResetSentinels 守护 PATCH 显式空字符串字段编码为 NUL 前缀 sentinel
// （与 ScoreThresholdResetSentinel 同构），且产物仍是合法 JSON。
func TestEncodeResetSentinels(t *testing.T) {
	raw := []byte(`{"reranking":"","rerank_model":"","judge_model":""}`)
	got := string(encodeResetSentinels(raw))
	if !strings.Contains(got, `"reranking":"\u0000rerank_reset"`) ||
		!strings.Contains(got, `"rerank_model":"\u0000rerank_model_reset"`) ||
		!strings.Contains(got, `"judge_model":"\u0000judge_model_reset"`) {
		t.Fatalf("sentinel encoding wrong: %s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("encoded body is not valid JSON: %s", got)
	}
}
