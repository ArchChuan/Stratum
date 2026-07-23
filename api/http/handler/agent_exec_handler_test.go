package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestExecuteAgentAndStreamDoneUseSameArtifactShape(t *testing.T) {
	result := &domain.AgentResult{AgentID: "a1", Input: "q", Output: "ok", Steps: 1, Duration: time.Second,
		Artifacts: []domain.ExecutionArtifact{{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []domain.EvidenceGap{}, RecommendedActions: []string{}, Citations: []domain.Citation{}, Steps: []domain.DiagnosticAreaResult{}}}}}
	syncDTO := agentExecutionResultDTO(result)
	done := agentExecutionDonePayload(result)
	var decoded map[string]any
	if err := json.Unmarshal(done, &decoded); err != nil {
		t.Fatal(err)
	}
	syncRaw, _ := json.Marshal(syncDTO.Artifacts)
	var syncDecoded any
	if err := json.Unmarshal(syncRaw, &syncDecoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(syncDecoded, decoded["artifacts"]) {
		t.Fatalf("artifact shapes drifted: sync=%v done=%v", syncDecoded, decoded["artifacts"])
	}
}

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
