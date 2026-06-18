// Package application exposes use-case services for the llmgateway context.
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// ModelService surfaces the available chat and embedding model names. The
// underlying catalogue is provided via a consumer-side port so HTTP
// handlers do not need to import the gateway infrastructure package.
type ModelService struct {
	catalog port.ModelCatalog
}

// NewModelService wires a ModelService with the provided catalog.
func NewModelService(catalog port.ModelCatalog) *ModelService {
	return &ModelService{catalog: catalog}
}

// Catalogue returns chat and embedding model names. Returned slices are
// never nil (callers can iterate without nil checks).
func (s *ModelService) Catalogue(_ context.Context) (chat, embedding []string) {
	chat = s.catalog.ListChatModels()
	if chat == nil {
		chat = []string{}
	}
	embedding = s.catalog.ListEmbeddingModels()
	if embedding == nil {
		embedding = []string{}
	}
	return chat, embedding
}
