package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/gin-gonic/gin"
)

type PlatformAssistantDocs interface {
	Search(context.Context, string) ([]domain.Citation, error)
}

type PlatformAssistantDiagnostics interface {
	Collect(context.Context, domain.DiagnosticRequest) (domain.DiagnosticEvidence, error)
}

type PlatformAssistantProposalInput struct {
	TenantID   string
	ActorID    string
	Kind       domain.ResourceKind
	Operation  domain.ProposalOperation
	ResourceID string
	Payload    json.RawMessage
}

type PlatformAssistantProposals interface {
	Create(context.Context, PlatformAssistantProposalInput) (domain.ResourceChangeProposalArtifact, error)
}

type PlatformAssistantCapabilityDeps struct {
	Docs        PlatformAssistantDocs
	Diagnostics PlatformAssistantDiagnostics
	Proposals   PlatformAssistantProposals
}

type PlatformAssistantCapabilityHandler struct {
	docs        PlatformAssistantDocs
	diagnostics PlatformAssistantDiagnostics
	proposals   PlatformAssistantProposals
}

func NewPlatformAssistantCapabilityHandler(
	deps PlatformAssistantCapabilityDeps,
) (*PlatformAssistantCapabilityHandler, error) {
	if deps.Docs == nil || deps.Diagnostics == nil || deps.Proposals == nil {
		return nil, errors.New("platform assistant capability dependencies are incomplete")
	}
	return &PlatformAssistantCapabilityHandler{
		docs: deps.Docs, diagnostics: deps.Diagnostics, proposals: deps.Proposals,
	}, nil
}

func (h *PlatformAssistantCapabilityHandler) SearchDocs(c *gin.Context) {
	var request struct {
		Query string `json:"query"`
	}
	if err := decodeClosedJSON(c, &request); err != nil {
		respondCapabilityBadRequest(c, err)
		return
	}
	query := strings.TrimSpace(request.Query)
	if query == "" || utf8.RuneCountInString(query) > constants.SystemAssistantQueryMaxRunes {
		respondCapabilityBadRequest(c, errors.New("invalid official docs query"))
		return
	}
	citations, err := h.docs.Search(c.Request.Context(), query)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"citations": domain.BoundCitations(citations)})
}

func (h *PlatformAssistantCapabilityHandler) DiagnoseTenant(c *gin.Context) {
	tenantID, actorID, ok := capabilityIdentity(c)
	if !ok {
		return
	}
	var request struct {
		Areas []domain.DiagnosticArea `json:"areas"`
	}
	if err := decodeClosedJSON(c, &request); err != nil {
		respondCapabilityBadRequest(c, err)
		return
	}
	areas, ok := validDiagnosticAreas(request.Areas)
	if !ok {
		respondCapabilityBadRequest(c, errors.New("invalid diagnostic areas"))
		return
	}
	evidence, err := h.diagnostics.Collect(c.Request.Context(), domain.DiagnosticRequest{
		TenantID: tenantID, UserID: actorID, Areas: areas,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"evidence": domain.BoundDiagnosticEvidence(evidence)})
}

func (h *PlatformAssistantCapabilityHandler) ProposeResourceChange(c *gin.Context) {
	tenantID, actorID, ok := capabilityIdentity(c)
	if !ok {
		return
	}
	var request struct {
		ResourceKind domain.ResourceKind      `json:"resourceKind"`
		Operation    domain.ProposalOperation `json:"operation"`
		ResourceID   string                   `json:"resourceId"`
		Payload      json.RawMessage          `json:"payload"`
	}
	if err := decodeClosedJSON(c, &request); err != nil || len(request.Payload) == 0 ||
		!request.ResourceKind.Valid() || !request.Operation.Valid() {
		respondCapabilityBadRequest(c, errors.New("invalid resource proposal request"))
		return
	}
	artifact, err := h.proposals.Create(c.Request.Context(), PlatformAssistantProposalInput{
		TenantID: tenantID, ActorID: actorID, Kind: request.ResourceKind,
		Operation: request.Operation, ResourceID: request.ResourceID, Payload: request.Payload,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, artifact)
}

func capabilityIdentity(c *gin.Context) (string, string, bool) {
	tenantID, tenantOK := tenantIDFromCtx(c)
	actorID, actorOK := userIDFromCtx(c)
	if !tenantOK || !actorOK || actorID == "" {
		_ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("delegated identity required")))
		return "", "", false
	}
	return tenantID, actorID, true
}

func respondCapabilityBadRequest(c *gin.Context, err error) {
	_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
}

func validDiagnosticAreas(areas []domain.DiagnosticArea) ([]domain.DiagnosticArea, bool) {
	if len(areas) == 0 || len(areas) > constants.SystemAssistantAreasMaxCount {
		return nil, false
	}
	seen := make(map[domain.DiagnosticArea]struct{}, len(areas))
	for _, area := range areas {
		if !area.Valid() {
			return nil, false
		}
		if _, exists := seen[area]; exists {
			return nil, false
		}
		seen[area] = struct{}{}
	}
	return areas, true
}
