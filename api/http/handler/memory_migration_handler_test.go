package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakeMemoryMigrationSvc 实现 memoryMigrationSvc，记录每次调用以便断言。
type fakeMemoryMigrationSvc struct {
	startTenant, startFrom, startTo string
	started                         *domain.MemoryMigration
	startErr                        error

	cancelTenant string
	cancelID     int64
	cancelErr    error

	retryTenant string
	retryID     int64
	retryErr    error

	current    *domain.MemoryMigration
	currentErr error

	cost    *application.MigrationCost
	costErr error
}

func (f *fakeMemoryMigrationSvc) StartMigration(_ context.Context, tenantID, fromModel, toModel string) (*domain.MemoryMigration, error) {
	f.startTenant, f.startFrom, f.startTo = tenantID, fromModel, toModel
	return f.started, f.startErr
}

func (f *fakeMemoryMigrationSvc) CancelMigration(_ context.Context, tenantID string, id int64) error {
	f.cancelTenant, f.cancelID = tenantID, id
	return f.cancelErr
}

func (f *fakeMemoryMigrationSvc) RetryMigration(_ context.Context, tenantID string, id int64) error {
	f.retryTenant, f.retryID = tenantID, id
	return f.retryErr
}

func (f *fakeMemoryMigrationSvc) GetCurrent(_ context.Context, _ string) (*domain.MemoryMigration, error) {
	return f.current, f.currentErr
}

func (f *fakeMemoryMigrationSvc) CostPreview(_ context.Context, _ string) (*application.MigrationCost, error) {
	return f.cost, f.costErr
}

func migTestRecord(id int64) *domain.MemoryMigration {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	return &domain.MemoryMigration{
		ID: id, TenantID: "t1",
		FromModel: "text-embedding-v1", ToModel: "text-embedding-v3",
		Status:   domain.MigrationStatusMigrating,
		Progress: 10, TotalFacts: 100,
		CreatedAt: now, UpdatedAt: now,
	}
}

func setupMemoryMigrationRouter(svc *fakeMemoryMigrationSvc, embed *fakeEmbedResolver, tenantID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))

	injectTenant := func(c *gin.Context) {
		if tenantID != "" {
			c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), tenantID))
		}
		c.Next()
	}

	h := NewMemoryMigrationHandler(svc, embed)
	mig := r.Group("/tenant/memory/migrations", injectTenant)
	mig.GET("/current", h.GetCurrent)
	mig.GET("/cost", h.GetCost)
	mig.POST("", h.Start)
	mig.POST("/:id/cancel", h.Cancel)
	mig.POST("/:id/retry", h.Retry)
	return r
}

func TestMemoryMigrationGetCurrent_Success(t *testing.T) {
	svc := &fakeMemoryMigrationSvc{current: migTestRecord(7)}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/memory/migrations/current", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["id"].(float64) != 7 {
		t.Errorf("id = %v, want 7", got["id"])
	}
	if got["from_model"] != "text-embedding-v1" || got["to_model"] != "text-embedding-v3" {
		t.Errorf("models = %v -> %v", got["from_model"], got["to_model"])
	}
	if got["status"] != "migrating" {
		t.Errorf("status = %v, want migrating", got["status"])
	}
	if got["progress"].(float64) != 10 || got["total_facts"].(float64) != 100 {
		t.Errorf("progress/total = %v/%v, want 10/100", got["progress"], got["total_facts"])
	}
}

func TestMemoryMigrationGetCurrent_None(t *testing.T) {
	r := setupMemoryMigrationRouter(&fakeMemoryMigrationSvc{}, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/memory/migrations/current", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := string(bytes.TrimSpace(w.Body.Bytes())); got != "null" {
		t.Fatalf("expected JSON null for no migration, got %s", got)
	}
}

func TestMemoryMigrationGetCurrent_ServiceError(t *testing.T) {
	r := setupMemoryMigrationRouter(
		&fakeMemoryMigrationSvc{currentErr: errors.New("db down")},
		&fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/memory/migrations/current", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationGetCost_Success(t *testing.T) {
	svc := &fakeMemoryMigrationSvc{cost: &application.MigrationCost{FactCount: 120, EstimatedSeconds: 24}}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/memory/migrations/cost", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["fact_count"].(float64) != 120 || got["estimated_seconds"].(float64) != 24 {
		t.Errorf("cost = %v facts / %v s, want 120/24", got["fact_count"], got["estimated_seconds"])
	}
}

func TestMemoryMigrationStart_Success(t *testing.T) {
	svc := &fakeMemoryMigrationSvc{started: migTestRecord(8)}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{model: "text-embedding-v1"}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations",
		bytes.NewBufferString(`{"to_model":"text-embedding-v3"}`)) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// from 模型由 resolver 解析，to 模型来自请求体。
	if svc.startFrom != "text-embedding-v1" || svc.startTo != "text-embedding-v3" {
		t.Errorf("started with %s -> %s, want v1 -> v3", svc.startFrom, svc.startTo)
	}
	if svc.startTenant != "t1" {
		t.Errorf("tenant = %s, want t1", svc.startTenant)
	}
}

func TestMemoryMigrationStart_MissingToModel(t *testing.T) {
	r := setupMemoryMigrationRouter(&fakeMemoryMigrationSvc{}, &fakeEmbedResolver{model: "text-embedding-v1"}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations",
		bytes.NewBufferString(`{"to_model":"  "}`)) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationStart_NoCurrentModel(t *testing.T) {
	// 租户未配置当前生效模型：无法确定迁移起点，fail-closed 400。
	r := setupMemoryMigrationRouter(&fakeMemoryMigrationSvc{}, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations",
		bytes.NewBufferString(`{"to_model":"text-embedding-v3"}`)) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationStart_AlreadyActive(t *testing.T) {
	// 已存在进行中迁移：service 返回 ErrMigrationAlreadyActive → 409。
	svc := &fakeMemoryMigrationSvc{startErr: domain.ErrMigrationAlreadyActive}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{model: "text-embedding-v1"}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations",
		bytes.NewBufferString(`{"to_model":"text-embedding-v3"}`)) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationCancel_Success(t *testing.T) {
	svc := &fakeMemoryMigrationSvc{}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations/7/cancel", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if svc.cancelID != 7 {
		t.Errorf("cancel id = %d, want 7", svc.cancelID)
	}
}

func TestMemoryMigrationCancel_InvalidID(t *testing.T) {
	r := setupMemoryMigrationRouter(&fakeMemoryMigrationSvc{}, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations/abc/cancel", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationCancel_NotActive(t *testing.T) {
	// done 状态的迁移不可取消：service 返回 ErrMigrationNotActive → 409。
	svc := &fakeMemoryMigrationSvc{cancelErr: domain.ErrMigrationNotActive}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations/7/cancel", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMemoryMigrationRetry_Success(t *testing.T) {
	svc := &fakeMemoryMigrationSvc{}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "t1")

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/memory/migrations/9/retry", nil) //nolint:noctx
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if svc.retryID != 9 {
		t.Errorf("retry id = %d, want 9", svc.retryID)
	}
}

func TestMemoryMigrationMissingTenant(t *testing.T) {
	// 无租户上下文：所有端点拒绝（401）。
	svc := &fakeMemoryMigrationSvc{current: migTestRecord(1)}
	r := setupMemoryMigrationRouter(svc, &fakeEmbedResolver{}, "")

	for _, path := range []string{
		"/tenant/memory/migrations/current",
		"/tenant/memory/migrations/cost",
	} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, path, nil) //nolint:noctx
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}
