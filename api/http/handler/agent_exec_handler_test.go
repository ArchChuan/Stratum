package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestAgentExecutionErrorUsesHTTPErrorPipeline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(zap.NewNop()))
	r.GET("/execute", func(c *gin.Context) {
		respondAgentExecutionError(c, errors.New("provider unavailable"))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/execute", nil)) //nolint:noctx
	if w.Code == http.StatusOK {
		t.Fatalf("agent failure returned HTTP 200: %s", w.Body.String())
	}
}

func TestExecuteStreamApprovalEventContainsOnlySafeBindingMetadata(t *testing.T) {
	payload := approvalRequiredSSEPayload(&port.ToolApprovalRequiredError{
		ApprovalID: "approval-1", ToolCallID: "call-1", ServerID: "orders",
		ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
	})
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"status", "approvalId", "toolCallId", "serverId", "toolName", "riskLevel"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("approval SSE payload missing %q: %s", key, payload)
		}
	}
	if len(decoded) != 6 {
		t.Fatalf("approval SSE payload contains unexpected fields: %s", payload)
	}
}
