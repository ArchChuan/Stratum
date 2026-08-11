package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeDLQReplayer struct {
	called    bool
	errorCode string
	result    MemoryDLQReplayResult
	err       error
}

func (f *fakeDLQReplayer) ReplayByErrorCode(_ context.Context, errorCode string) (MemoryDLQReplayResult, error) {
	f.called = true
	f.errorCode = errorCode
	return f.result, f.err
}

func setupDLQReplayRouter(svc dlqReplayer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	h := NewMemoryDlqReplayHandler(svc)
	r.POST("/admin/memory/dlq/replay", h.Replay)
	return r
}

func TestMemoryDLQReplay_PassesErrorCodeAndRendersResult(t *testing.T) {
	svc := &fakeDLQReplayer{result: MemoryDLQReplayResult{Total: 3, Replayed: 1, Skipped: 2, Failed: 0}}
	r := setupDLQReplayRouter(svc)

	body := bytes.NewBufferString(`{"errorCode":"embed_service_unavailable"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/memory/dlq/replay", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, svc.called)
	assert.Equal(t, "embed_service_unavailable", svc.errorCode)
	var got MemoryDLQReplayResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, MemoryDLQReplayResult{Total: 3, Replayed: 1, Skipped: 2}, got)
}

func TestMemoryDLQReplay_RejectsMissingErrorCode(t *testing.T) {
	r := setupDLQReplayRouter(&fakeDLQReplayer{})

	req := httptest.NewRequest(http.MethodPost, "/admin/memory/dlq/replay",
		bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMemoryDLQReplay_PropagatesServiceError(t *testing.T) {
	svc := &fakeDLQReplayer{err: errors.New("nats down")}
	r := setupDLQReplayRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/admin/memory/dlq/replay",
		bytes.NewBufferString(`{"errorCode":"embed_service_unavailable"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
