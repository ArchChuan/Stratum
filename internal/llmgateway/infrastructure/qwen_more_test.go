package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestNewQwenClient(t *testing.T) {
	client := NewQwenClient("sk-test", zap.NewNop())
	require.NotNil(t, client)
	require.Equal(t, "qwen", client.cfg.Name)
	require.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", client.cfg.BaseURL)
	require.Equal(t, "sk-test", client.cfg.APIKey)
	require.Equal(t, 10, client.cfg.EmbedBatchSize)
}

func TestRepoConstructors(t *testing.T) {
	require.NotNil(t, NewPgModelRepo(nil))
	require.NotNil(t, NewPgProviderRepo(nil, testAESKey, zap.NewNop(), observability.NoopMetrics{}))
}
