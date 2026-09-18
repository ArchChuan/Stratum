package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/api/middleware"
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// stopControlBus 记录发布过的停止指令。
type stopControlBus struct {
	stops []string
}

func (b *stopControlBus) PublishStop(_ context.Context, executionID string) error {
	b.stops = append(b.stops, executionID)
	return nil
}

func (b *stopControlBus) PublishViewer(context.Context, string, string) error { return nil }

func (b *stopControlBus) Subscribe(context.Context, string) (<-chan port.ControlMessage, func(), error) {
	ch := make(chan port.ControlMessage)
	return ch, func() {}, nil
}

func TestStopExecutionPublishesStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	store := &stubCheckpointStore{userID: "u1"}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: store,
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil)
	c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "t1"))
	c.Params = gin.Params{{Key: "executionID", Value: "e1"}}
	c.Set(middleware.ContextKeySub, "u1")

	h.StopExecution(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(bus.stops) != 1 || bus.stops[0] != "e1" {
		t.Fatalf("published stops = %v, want [e1]", bus.stops)
	}
	// 租户与 execution 必须逐字透传到应用层的租户限定查询。
	if store.gotTenantID != "t1" || store.gotExecutionID != "e1" {
		t.Fatalf("checkpoint lookup = (%q,%q), want (t1,e1)", store.gotTenantID, store.gotExecutionID)
	}
	if !strings.Contains(rec.Body.String(), `"stopping"`) {
		t.Fatalf("body = %q, want stopping", rec.Body.String())
	}
}

func TestStopExecutionHidesForeignExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{userID: "someone-else"},
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	// 归属失败只走 c.Error，状态码由 ErrorHandler 渲染：直接调 handler 时
	// recorder 保持默认 200，断言「非 200」会失去判别力。因此这里走真实路由。
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "t1"))
		c.Set(middleware.ContextKeySub, "u1")
		c.Next()
	})
	router.POST("/agents/:id/executions/:executionID/stop", h.StopExecution)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil))

	// 别人的执行必须 404，且不能发出停止指令——否则任何人都能停掉任何执行。
	if len(bus.stops) != 0 {
		t.Fatalf("stop published for a foreign execution: %v", bus.stops)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a foreign execution", rec.Code)
	}
}

// 裁定 8：JWT 中间件无条件 c.Set(ContextKeySub, claims.Sub)，sub 为空串时
// userIDFromCtx 仍返回 ok=true（类型断言成功）。handler 必须显式判空，否则
// 空 actor 会穿过归属判定停掉同租户内任意执行。
//
// fixture 取 cp.UserID == ""（而不是某个具名 owner）：两侧同空正是逐字处方
// `cp.UserID != userID` 会放行的退化输入，也是「空 sub 令牌自建执行」的真实形态。
// 零发布断言因此能咬住「判空被移除」这条泄漏路径，而不只是 401/404 的差异。
func TestStopExecutionRejectsEmptyActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	store := &stubCheckpointStore{userID: ""}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: store,
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "t1"))
		// 空 sub：ok 为 true 但值为空——只判 !ok 堵不住这个缺陷。
		c.Set(middleware.ContextKeySub, "")
		c.Next()
	})
	router.POST("/agents/:id/executions/:executionID/stop", h.StopExecution)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil))

	if len(bus.stops) != 0 {
		t.Fatalf("stop published for an empty actor: %v", bus.stops)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an empty actor", rec.Code)
	}
	// 身份闸门必须排在 checkpoint 查询之前：身份缺失时不得触碰任何存储 IO。
	if store.calls != 0 {
		t.Fatalf("checkpoint store queried %d times for a missing actor, want 0", store.calls)
	}
}

// GetLatest 出错时必须把故障暴露成 5xx，而不是静默当成「没有这条执行」——
// 后者会让一次 DB 抖动退化成「查无此执行」的错误语义。同域 stop 语义：
// 存在性与归属合并为一次租户限定查询，IO 失败必须向上传播（红线第 5 条）。
func TestStopExecutionSurfacesCheckpointLoadFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{err: errors.New("checkpoint store down")},
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "t1"))
		c.Set(middleware.ContextKeySub, "u1")
		c.Next()
	})
	router.POST("/agents/:id/executions/:executionID/stop", h.StopExecution)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil))

	if len(bus.stops) != 0 {
		t.Fatalf("stop published despite a failed checkpoint lookup: %v", bus.stops)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
	}
	// 上游原始错误文本不得进入响应体。
	if strings.Contains(rec.Body.String(), "checkpoint store down") {
		t.Fatalf("body leaked the raw upstream error: %s", rec.Body.String())
	}
}

// stubCheckpointStore 只实现 StopExecution 需要的 GetLatest。
//
// 记录收到的 (tenantID, executionID)：红线第 3 条要求租户作用域查询显式携带
// 并校验 tenant，只靠阅读 handler 的逐字透传无法防回归，桩必须把入参钉住。
type stubCheckpointStore struct {
	userID string
	err    error

	gotTenantID    string
	gotExecutionID string
	calls          int
}

func (s *stubCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}

func (s *stubCheckpointStore) GetLatest(
	_ context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotExecutionID = executionID
	if s.err != nil {
		return nil, s.err
	}
	return &domain.AgentExecutionCheckpoint{ExecutionID: executionID, UserID: s.userID}, nil
}

func (s *stubCheckpointStore) MarkCompleted(context.Context, string, string) error { return nil }
func (s *stubCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	return nil
}
func (s *stubCheckpointStore) DeleteExpired(context.Context, string) (int64, error) { return 0, nil }
func (s *stubCheckpointStore) GetLatestActiveByConversation(
	context.Context, string, string,
) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (s *stubCheckpointStore) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubCheckpointStore) AdvanceRunGeneration(context.Context, string, string, int) error {
	return nil
}
func (s *stubCheckpointStore) Terminate(context.Context, string, string, string) error { return nil }
