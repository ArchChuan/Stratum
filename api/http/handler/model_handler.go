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

// ListModels GET /models — returns chat and embedding model names, sorted, no auth required.
func (h *ModelHandler) ListModels(c *gin.Context) {
	chat, embedding := h.svc.Catalogue(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"models": chat, "embedding_models": embedding})
}
