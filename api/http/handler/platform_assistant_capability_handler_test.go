package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type capabilityDocsFake struct{}

func (capabilityDocsFake) Search(context.Context, string) ([]domain.Citation, error) {
	return []domain.Citation{{Title: "Agent", URL: "/docs/agent", Excerpt: "managed"}}, nil
}

type capabilityDiagnosticFake struct {
	request domain.DiagnosticRequest
}

func (f *capabilityDiagnosticFake) Collect(
	_ context.Context,
	request domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	f.request = request
	return domain.DiagnosticEvidence{Gaps: []domain.EvidenceGap{{
		Area: domain.DiagnosticAreaMCP, Code: domain.DiagnosticGapUnavailable,
	}}}, nil
}

type capabilityProposalFake struct {
	input PlatformAssistantProposalInput
}

func (f *capabilityProposalFake) Create(
	_ context.Context,
	input PlatformAssistantProposalInput,
) (domain.ResourceChangeProposalArtifact, error) {
	f.input = input
	return domain.ResourceChangeProposalArtifact{ID: "proposal-1", Status: domain.StatusReadyForReview}, nil
}

func TestPlatformAssistantCapabilityHandlerExecutesClosedCapabilities(t *testing.T) {
	diagnostics := &capabilityDiagnosticFake{}
	proposals := &capabilityProposalFake{}
	handler, err := NewPlatformAssistantCapabilityHandler(PlatformAssistantCapabilityDeps{
		Docs: capabilityDocsFake{}, Diagnostics: diagnostics, Proposals: proposals,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, body string
		assert           func(*testing.T, map[string]any)
	}{
		{name: "docs", path: "/docs", body: `{"query":"Agent"}`, assert: func(t *testing.T, body map[string]any) {
			if len(body["citations"].([]any)) != 1 {
				t.Fatalf("body=%v", body)
			}
		}},
		{name: "diagnostics", path: "/diagnostics", body: `{"areas":["mcp"]}`, assert: func(t *testing.T, body map[string]any) {
			if body["evidence"] == nil || diagnostics.request.TenantID != "tenant-1" || diagnostics.request.UserID != "user-1" {
				t.Fatalf("body=%v request=%+v", body, diagnostics.request)
			}
		}},
		{name: "proposal", path: "/proposals", body: `{"resourceKind":"agent","operation":"create","payload":{"name":"A"}}`, assert: func(t *testing.T, body map[string]any) {
			if body["id"] != "proposal-1" || proposals.input.TenantID != "tenant-1" || proposals.input.ActorID != "user-1" {
				t.Fatalf("body=%v input=%+v", body, proposals.input)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := performCapabilityRequest(handler, tc.path, tc.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			tc.assert(t, body)
		})
	}
}

func TestPlatformAssistantCapabilityHandlerRejectsUnknownFields(t *testing.T) {
	handler, err := NewPlatformAssistantCapabilityHandler(PlatformAssistantCapabilityDeps{
		Docs: capabilityDocsFake{}, Diagnostics: &capabilityDiagnosticFake{}, Proposals: &capabilityProposalFake{},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performCapabilityRequest(handler, "/docs", `{"query":"Agent","url":"https://copycat"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func performCapabilityRequest(
	handler *PlatformAssistantCapabilityHandler,
	path, body string,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeySub, "user-1")
		c.Request = c.Request.WithContext(reqctx.WithTenantID(c.Request.Context(), "tenant-1"))
	})
	router.POST("/docs", handler.SearchDocs)
	router.POST("/diagnostics", handler.DiagnoseTenant)
	router.POST("/proposals", handler.ProposeResourceChange)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
