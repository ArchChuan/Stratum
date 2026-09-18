package handler

import (
	"context"
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
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{userID: "u1"},
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
func TestStopExecutionRejectsEmptyActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{userID: "u1"},
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
}

// stubCheckpointStore 只实现 StopExecution 需要的 GetLatest。
type stubCheckpointStore struct {
	userID string
	err    error
}

func (s *stubCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}

func (s *stubCheckpointStore) GetLatest(
	context.Context, string, string,
) (*domain.AgentExecutionCheckpoint, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: s.userID}, nil
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
