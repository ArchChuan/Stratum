package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api/middleware"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestExecuteAgentAndStreamDoneUseSameArtifactShape(t *testing.T) {
	result := &domain.AgentResult{AgentID: "a1", Input: "q", Output: "ok", Steps: 1, Duration: time.Second,
		Artifacts: []domain.ExecutionArtifact{{Type: "diagnostic_report", ProfileVersion: "v1", DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []domain.EvidenceGap{}, RecommendedActions: []string{}, Citations: []domain.Citation{}, Steps: []domain.DiagnosticStep{}}}}}
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

func TestAgentExecutionErrorPayloadUsesPublicContract(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		want      map[string]string
		forbidden string
	}{
		{
			name: "assistant model unavailable",
			err:  fmt.Errorf("assemble options: %w", domain.ErrAssistantModelUnavailable),
			want: map[string]string{
				"error": "租户尚未配置平台助手模型",
				"code":  middleware.CodeSystemAssistantModelUnavailable,
			},
			forbidden: "assemble options",
		},
		{
			name:      "unknown server error",
			err:       errors.New("provider api_key=do-not-leak"),
			want:      map[string]string{"error": "internal server error"},
			forbidden: "do-not-leak",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := agentExecutionErrorPayload(tt.err)
			var decoded map[string]string
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, tt.want) {
				t.Fatalf("payload = %#v, want %#v", decoded, tt.want)
			}
			if strings.Contains(string(payload), tt.forbidden) {
				t.Fatalf("payload leaked %q: %s", tt.forbidden, payload)
			}
		})
	}
}

func TestExecuteAgentStreamReturnsJSONContractBeforeStreamStarts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingsAgentRepo{cfg: &domain.AgentConfig{
		ID: domain.SystemAssistantID, SystemKey: domain.SystemAssistantKey,
	}}
	registry := agentapp.NewRegistry(repo, agentapp.BuiltinSystemAssistantProfileSource(), zap.NewNop())
	svc := agentapp.NewAgentService(agentapp.AgentServiceDeps{Registry: registry, Logger: zap.NewNop()})
	handler := NewAgentHandler(svc, zap.NewNop())
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		ctx := reqctx.WithTenantID(c.Request.Context(), "tenant-1")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.POST("/agents/:id/execute/stream", handler.ExecuteAgentStream)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/agents/"+domain.SystemAssistantID+"/execute/stream",
		bytes.NewBufferString(`{"query":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	wantBody := "{\"code\":\"SYSTEM_ASSISTANT_MODEL_UNAVAILABLE\",\"error\":\"租户尚未配置平台助手模型\"}"
	if response.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", response.Body.String(), wantBody)
	}
}

func TestAgentExecutionDonePayloadSourcesSerializeAsArray(t *testing.T) {
	// Without sources the payload must carry "sources":[] — never null — so
	// the frontend can treat done.sources as a list during rolling upgrades.
	result := &domain.AgentResult{AgentID: "a1", Output: "ok"}
	done := agentExecutionDonePayload(result)
	if !strings.Contains(string(done), `"sources":[]`) {
		t.Fatalf("done payload must serialize empty sources as []: %s", done)
	}
	if strings.Contains(string(done), `"sources":null`) {
		t.Fatalf("done payload must never serialize null sources: %s", done)
	}

	// With sources, fields serialize camelCase and Snippet rides along.
	result.Sources = []domain.RAGSearchSource{{
		WorkspaceID: "ws-1", WorkspaceName: "legal", ChunkID: "chunk-1",
		DocumentID: "doc-1", DocumentTitle: "policy.pdf", Snippet: "must be short",
	}}
	done = agentExecutionDonePayload(result)
	var decoded map[string]any
	if err := json.Unmarshal(done, &decoded); err != nil {
		t.Fatal(err)
	}
	sources, ok := decoded["sources"].([]any)
	if !ok || len(sources) != 1 {
		t.Fatalf("sources = %#v, want 1 item", decoded["sources"])
	}
	first, ok := sources[0].(map[string]any)
	if !ok {
		t.Fatalf("source = %#v, want object", sources[0])
	}
	for _, key := range []string{"workspaceId", "workspaceName", "chunkId", "documentId", "documentTitle", "snippet"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("source missing %q: %#v", key, first)
		}
	}
}
