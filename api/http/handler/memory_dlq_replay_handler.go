package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/byteBuilderX/stratum/api/middleware"
)

// MemoryDLQReplayResult 是 DLQ 定向重放的结果汇总。
type MemoryDLQReplayResult struct {
	Total    int `json:"total"`
	Replayed int `json:"replayed"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// dlqReplayer 按 error_code 重放死信事件。消费方侧接口：
// handler 不 import infrastructure，具体实现由 router 层以
// pipeline.ReplayService 的薄适配提供。
type dlqReplayer interface {
	ReplayByErrorCode(ctx context.Context, errorCode string) (MemoryDLQReplayResult, error)
}

// MemoryDlqReplayHandler 提供 DLQ 定向重放管理接口（global admin）。
type MemoryDlqReplayHandler struct {
	svc dlqReplayer
}

// NewMemoryDlqReplayHandler 构造 DLQ 重放 handler。
func NewMemoryDlqReplayHandler(svc dlqReplayer) *MemoryDlqReplayHandler {
	return &MemoryDlqReplayHandler{svc: svc}
}

// Replay POST /admin/memory/dlq/replay — 定向重放死信事件。
// tenantID 一律由事件自身派生，请求体不接受租户参数，防止跨租户重放。
func (h *MemoryDlqReplayHandler) Replay(c *gin.Context) {
	var req struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	if req.ErrorCode == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, fmt.Errorf("errorCode is required")))
		return
	}
	result, err := h.svc.ReplayByErrorCode(c.Request.Context(), req.ErrorCode)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, result)
}
