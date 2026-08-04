package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticModelCatalog_ListChatModels(t *testing.T) {
	models := StaticModelCatalog{}.ListChatModels()
	require.NotEmpty(t, models)
	require.Contains(t, models, "qwen-turbo")
	require.Contains(t, models, "glm-4")
}

func TestStaticModelCatalog_ListEmbeddingModels(t *testing.T) {
	models := StaticModelCatalog{}.ListEmbeddingModels()
	require.NotEmpty(t, models)
	require.Contains(t, models, "text-embedding-v3")
	require.Contains(t, models, "embedding-3")
}
