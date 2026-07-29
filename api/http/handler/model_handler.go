package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
)

// ModelHandler serves available LLM model information.
type ModelHandler struct {
	svc *llmapp.ModelService
}

// NewModelHandler creates a ModelHandler.
func NewModelHandler(svc *llmapp.ModelService) *ModelHandler {
	return &ModelHandler{svc: svc}
}

// ListModels GET /models returns the authenticated tenant's eligible chat and embedding models.
func (h *ModelHandler) ListModels(c *gin.Context) {
	var chat, embedding []string
	if h.svc == nil {
		chat = []string{}
		embedding = []string{}
	} else if tid, ok := tenantIDFromCtx(c); ok {
		chat, embedding = h.svc.CatalogueWithTenant(c.Request.Context(), tid)
	} else {
		chat = []string{}
		embedding = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"models": chat, "embedding_models": embedding})
}
