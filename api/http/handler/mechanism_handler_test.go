package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/dto/gen"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// mechanismFakeRepo 是 ProfileRepo 的内存替身（测试只 mock 外部依赖）。
// Upsert 模拟真实 SQL 语义：族键冲突时 version 自增；upsertErr 独立于 err
// 注入 Upsert 失败，err 注入 List/GetByFamilyKey 失败（打开 readback 500 路径）。
type mechanismFakeRepo struct {
	profiles  []mechanismdomain.Profile
	err       error
	upsertErr error
}

func (f *mechanismFakeRepo) GetByFamilyKey(_ context.Context, familyKey string) (mechanismdomain.Profile, bool, error) {
	if f.err != nil {
		return mechanismdomain.Profile{}, false, f.err
	}
	for _, p := range f.profiles {
		if p.FamilyKey == familyKey {
			return p, true, nil
		}
	}
	return mechanismdomain.Profile{}, false, nil
}

func (f *mechanismFakeRepo) List(_ context.Context) ([]mechanismdomain.Profile, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.profiles, nil
}

func (f *mechanismFakeRepo) Upsert(_ context.Context, p mechanismdomain.Profile) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	for i, existing := range f.profiles {
		if existing.FamilyKey == p.FamilyKey {
			// 与 profile_repo.go 的 version=model_profiles.version+1 语义一致。
			p.Version = existing.Version + 1
			f.profiles[i] = p
			return nil
		}
	}
	f.profiles = append(f.profiles, p)
	return nil
}

func mechanismProfile() mechanismdomain.Profile {
	return mechanismdomain.Profile{
		ID:          "p1",
		FamilyKey:   "qwen",
		DisplayName: "通义千问",
		Matcher:     mechanismdomain.ModelMatcher{FamilyPrefixes: []string{"qwen"}},
		Status:      mechanismdomain.ProfileStatusActive,
		Version:     3,
		Baseline: mechanismdomain.Baseline{
			Prompts: mechanismdomain.BaselinePrompts{Compaction: "压缩指令"},
			Models:  mechanismdomain.BaselineModels{EnrichModel: "qwen-max"},
		},
	}
}

func newMechanismTestRouter(repo *mechanismFakeRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewMechanismHandler(application.NewService(repo), zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	g := r.Group("/mechanism/profiles")
	g.GET("", h.List)
	g.GET("/:familyKey", h.Get)
	g.PUT("", h.Upsert)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, _ := http.NewRequest(method, path, &buf) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMechanismHandler_List_returnsProfiles(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodGet, "/mechanism/profiles", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.ListProfilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Profiles) != 1 || resp.Profiles[0].FamilyKey != "qwen" {
		t.Fatalf("unexpected profiles: %+v", resp.Profiles)
	}
	if resp.Profiles[0].Baseline.Compaction != "压缩指令" || resp.Profiles[0].Baseline.EnrichModel != "qwen-max" {
		t.Fatalf("baseline lost in mapping: %+v", resp.Profiles[0].Baseline)
	}
}

func TestMechanismHandler_Get_found(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodGet, "/mechanism/profiles/qwen", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp gen.ProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != 3 || resp.Status != "active" {
		t.Fatalf("unexpected profile: %+v", resp)
	}
}

func TestMechanismHandler_Get_notFound(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodGet, "/mechanism/profiles/nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestMechanismHandler_Upsert_persistsAndReturns(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}}
	body := gen.UpsertProfileRequest{
		FamilyKey:      "qwen",
		DisplayName:    "通义千问新",
		FamilyPrefixes: []string{"qwen", "qwen2"},
		Status:         "draft",
		Baseline: gen.ProfileBaselineRequest{
			Compaction:  "新压缩指令",
			EnrichModel: "qwen-max-latest",
		},
	}
	router := newMechanismTestRouter(repo)
	w := doJSON(t, router, http.MethodPut, "/mechanism/profiles", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.ProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DisplayName != "通义千问新" || resp.Baseline.Compaction != "新压缩指令" {
		t.Fatalf("unexpected upsert result: %+v", resp)
	}
	if len(repo.profiles) != 1 {
		t.Fatalf("expected single profile, got %d", len(repo.profiles))
	}
	if repo.profiles[0].Fingerprint == "" {
		t.Fatal("fingerprint should be computed on upsert")
	}
}

func TestMechanismHandler_Upsert_validationError(t *testing.T) {
	repo := &mechanismFakeRepo{}
	body := gen.UpsertProfileRequest{FamilyKey: "qwen"} // family_prefixes 缺失
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodPut, "/mechanism/profiles", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMechanismHandler_Upsert_bindsActor(t *testing.T) {
	repo := &mechanismFakeRepo{}
	// 带 auth.sub 上下文验证 created_by 记录操作者。
	w := doJSON(t, newMechanismTestRouterWithActor(repo, "u-42"), http.MethodPut, "/mechanism/profiles",
		gen.UpsertProfileRequest{FamilyKey: "glm", FamilyPrefixes: []string{"glm"}})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if repo.profiles[0].CreatedBy != "u-42" {
		t.Fatalf("expected created_by=u-42, got %q", repo.profiles[0].CreatedBy)
	}
	if repo.profiles[0].Version != 1 {
		t.Fatalf("expected first version=1, got %d", repo.profiles[0].Version)
	}
}

func newMechanismTestRouterWithActor(repo *mechanismFakeRepo, actor string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewMechanismHandler(application.NewService(repo), zap.NewNop())
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	g := r.Group("/mechanism/profiles", func(c *gin.Context) { c.Set(middleware.ContextKeySub, actor) })
	g.GET("", h.List)
	g.GET("/:familyKey", h.Get)
	g.PUT("", h.Upsert)
	return r
}

func TestMechanismHandler_Upsert_incrementsVersionOnConflict(t *testing.T) {
	repo := &mechanismFakeRepo{profiles: []mechanismdomain.Profile{mechanismProfile()}} // Version=3
	body := gen.UpsertProfileRequest{FamilyKey: "qwen", FamilyPrefixes: []string{"qwen"}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodPut, "/mechanism/profiles", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.ProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != 4 {
		t.Fatalf("expected version 3→4 on conflict, got %d", resp.Version)
	}
}

func TestMechanismHandler_Upsert_returns500WhenRepoFails(t *testing.T) {
	repo := &mechanismFakeRepo{upsertErr: errors.New("db down")}
	body := gen.UpsertProfileRequest{FamilyKey: "qwen", FamilyPrefixes: []string{"qwen"}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodPut, "/mechanism/profiles", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestMechanismHandler_Upsert_returns500WhenReadbackFails(t *testing.T) {
	// Upsert 成功但紧随的 GetByFamilyKey 失败 → 500，不能伪成功返回。
	repo := &mechanismFakeRepo{err: errors.New("db down")}
	body := gen.UpsertProfileRequest{FamilyKey: "qwen", FamilyPrefixes: []string{"qwen"}}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodPut, "/mechanism/profiles", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestMechanismHandler_List_returns500WhenRepoFails(t *testing.T) {
	repo := &mechanismFakeRepo{err: errors.New("db down")}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodGet, "/mechanism/profiles", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestMechanismHandler_Get_returns500WhenRepoFails(t *testing.T) {
	repo := &mechanismFakeRepo{err: errors.New("db down")}
	w := doJSON(t, newMechanismTestRouter(repo), http.MethodGet, "/mechanism/profiles/qwen", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
