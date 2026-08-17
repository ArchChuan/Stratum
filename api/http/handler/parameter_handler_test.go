package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	paramapp "github.com/byteBuilderX/stratum/internal/parameters/application"
	paramdomain "github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// fakePlatformStore implements port.PlatformStore for handler tests; each
// method is wired through a function field so tests can override behaviour.
type fakePlatformStore struct {
	getFn  func(context.Context, string) (json.RawMessage, bool, error)
	setFn  func(context.Context, string, json.RawMessage, string) error
	allFn  func(context.Context) ([]port.PlatformValue, error)
	values map[string]json.RawMessage
}

func (f *fakePlatformStore) GetValue(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if f.getFn != nil {
		return f.getFn(ctx, key)
	}
	if f.values == nil {
		return nil, false, nil
	}
	raw, ok := f.values[key]
	return raw, ok, nil
}

func (f *fakePlatformStore) SetValue(ctx context.Context, key string, value json.RawMessage, updatedBy string) error {
	if f.setFn != nil {
		return f.setFn(ctx, key, value, updatedBy)
	}
	if f.values == nil {
		f.values = make(map[string]json.RawMessage)
	}
	f.values[key] = value
	return nil
}

func (f *fakePlatformStore) GetAll(ctx context.Context) ([]port.PlatformValue, error) {
	if f.allFn != nil {
		return f.allFn(ctx)
	}
	var out []port.PlatformValue
	for k, v := range f.values {
		out = append(out, port.PlatformValue{Key: k, Value: v})
	}
	return out, nil
}

func setupParameterRouter(h *ParameterHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/parameters/prompt-defaults", h.PromptDefaults)
	r.GET("/admin/parameters", h.List)
	r.PUT("/admin/parameters", h.Update)
	return r
}

func newTestParameterHandler(store port.PlatformStore) *ParameterHandler {
	return NewParameterHandler(
		paramapp.NewService(paramdomain.NewParametersRegistry(), store),
		zap.NewNop(),
	)
}

func TestPromptDefaults_returnsAllWhitelistedTemplates(t *testing.T) {
	r := setupParameterRouter(newTestParameterHandler(&fakePlatformStore{}))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/parameters/prompt-defaults", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"agent.compaction_prompt",
		"memory.extraction_prompt",
		"memory.enrich_prompt",
		"memory.summary_prompt",
		"memory.history_summary_prompt",
		"memory.supersede_prompt",
	}
	for _, key := range wantKeys {
		got, ok := resp[key]
		if !ok {
			t.Fatalf("missing key %s in response %v", key, resp)
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("key %s is empty", key)
		}
	}
	// 模板与 pkg/constants 逐字节一致，防止搬移漂移。
	if resp["memory.extraction_prompt"] != constants.MemoryExtractionDefaultPrompt {
		t.Fatal("memory.extraction_prompt 与 constants.MemoryExtractionDefaultPrompt 不一致")
	}
	if resp["agent.compaction_prompt"] != constants.CompactionDefaultPrompt {
		t.Fatal("agent.compaction_prompt 与 constants.CompactionDefaultPrompt 不一致")
	}
	// 占位符契约：运行时替换的 %s/%d 必须保留在下发模板中。
	if !strings.Contains(resp["memory.extraction_prompt"], "%s") || !strings.Contains(resp["memory.extraction_prompt"], "%d") {
		t.Fatal("memory.extraction_prompt 缺少运行时可替换的占位符")
	}
}

func TestListPlatformValues_returnsStoredAndNonZeroDefaults(t *testing.T) {
	store := &fakePlatformStore{
		values: map[string]json.RawMessage{
			"memory.enrich_temperature": json.RawMessage(`0.9`),
		},
	}
	r := setupParameterRouter(newTestParameterHandler(store))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/parameters", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := resp["memory.enrich_temperature"]; got != float64(0.9) {
		t.Fatalf("expected stored 0.9, got %v", got)
	}
	// 非 0 默认值必须回填（List 语义：缺失键 = 0/''/nil 默认）。
	if _, ok := resp["memory.supersede_temperature"]; ok {
		t.Fatal("0 默认键不应出现在 List 返回值中")
	}
}

func TestUpdatePlatformValues_writesOnlyGivenKeys(t *testing.T) {
	store := &fakePlatformStore{}
	r := setupParameterRouter(newTestParameterHandler(store))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/admin/parameters", strings.NewReader(`{"memory.enrich_temperature":0.9}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(store.values) != 1 {
		t.Fatalf("expected exactly 1 stored key, got %v", store.values)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := resp["memory.enrich_temperature"]; got != float64(0.9) {
		t.Fatalf("expected merged 0.9, got %v", got)
	}
}

func TestUpdatePlatformValues_rejectsUnknownKey(t *testing.T) {
	r := setupParameterRouter(newTestParameterHandler(&fakePlatformStore{}))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/admin/parameters", strings.NewReader(`{"not.a.real.key":1}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
