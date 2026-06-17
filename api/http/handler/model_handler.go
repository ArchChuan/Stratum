package handler

import (
	"net/http"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/gin-gonic/gin"
)

// ModelHandler serves available LLM model information.
type ModelHandler struct {
	gateway *llmgateway.Gateway
}

// NewModelHandler creates a ModelHandler.
func NewModelHandler(gateway *llmgateway.Gateway) *ModelHandler {
	return &ModelHandler{gateway: gateway}
}

// ListModels GET /models — returns chat and embedding model names, sorted, no auth required.
func (h *ModelHandler) ListModels(c *gin.Context) {
	models := h.gateway.ListChatModels()
	if models == nil {
		models = []string{}
	}
	embeddingModels := h.gateway.ListEmbeddingModels()
	if embeddingModels == nil {
		embeddingModels = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "embedding_models": embeddingModels})
}
