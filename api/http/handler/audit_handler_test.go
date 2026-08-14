package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type stubAuditQuery struct {
	rows  []auditport.ResourceChangeAuditRow
	total int
	byID  *auditport.ResourceChangeAuditRow
}

func (s *stubAuditQuery) List(context.Context, string, auditport.ResourceChangeAuditFilter) ([]auditport.ResourceChangeAuditRow, int, error) {
	return s.rows, s.total, nil
}

func (s *stubAuditQuery) GetByID(context.Context, string, string) (*auditport.ResourceChangeAuditRow, error) {
	return s.byID, nil
}

// newAuditTestRouter 注入租户到 request context（tenantIDFromCtx 从 reqctx
// 读取，不是 gin.Context），与 workflow_handler_test.go 的注入模式一致。
func newAuditTestRouter(h *AuditHandler) *gin.Engine {
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
		c.Next()
	})
	r.GET("/audit/events", h.ListEvents)
	r.GET("/audit/events/:id", h.GetEvent)
	return r
}

func TestAuditHandler_ListEvents(t *testing.T) {
	h := NewAuditHandler(&stubAuditQuery{
		rows: []auditport.ResourceChangeAuditRow{
			{ID: "a1", ResourceKind: "workflow", ResourceID: "wf-1", Operation: "publish",
				ActorID: "u-1", ActorName: "李雷", CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
				Before: json.RawMessage(`{}`), After: json.RawMessage(`{"id":"wf-1"}`)},
		},
		total: 1,
	}, zap.NewNop())
	r := newAuditTestRouter(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/audit/events?resource_kind=workflow&page=1&page_size=20", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(1), body["total"])
	events := body["events"].([]any)
	first := events[0].(map[string]any)
	require.Equal(t, "李雷", first["actor_name"]) // gen DTO json tag 为 snake_case
	require.Equal(t, "wf-1", first["resource_id"])
}

func TestAuditHandler_GetEvent_NotFound(t *testing.T) {
	h := NewAuditHandler(&stubAuditQuery{}, zap.NewNop())
	r := newAuditTestRouter(h)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/audit/events/missing", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}
