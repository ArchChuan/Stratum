package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceSnapshotRoundTrip(t *testing.T) {
	ws := &Workspace{
		ID:          "ws-1",
		Name:        "知识库 A",
		Description: "内部文档",
		Config: WorkspaceConfig{
			EmbeddingModel:   "text-embedding-v3",
			ChunkSize:        512,
			ChunkOverlap:     64,
			QueryMode:        "hybrid",
			TopK:             10,
			ChunkingStrategy: "recursive",
			Reranking:        "provider:reranker",
			ScoreThreshold:   0.7,
			RerankTopK:       5,
			RerankModel:      "qwen-rerank",
			JudgeModel:       "qwen-judge",
		},
		CreatedBy: "u1",
	}

	snap := SnapshotFromWorkspace(ws)
	payload := snap.Map()
	// payload 必须是可哈希的 canonical JSON：无缺失、无额外键。
	got, err := SnapshotFromMap(payload)
	require.NoError(t, err)
	require.Equal(t, "知识库 A", got.Name)
	require.Equal(t, "内部文档", got.Description)
	require.Equal(t, ws.Config, got.Config)
	require.Equal(t, ws.Name, got.ToWorkspace(ws.ID).Name)
	require.Equal(t, ws.Description, got.ToWorkspace(ws.ID).Description)
	require.Equal(t, ws.Config, got.ToWorkspace(ws.ID).Config)
	require.Equal(t, ws.ID, got.ToWorkspace(ws.ID).ID)
}

func TestWorkspaceSnapshotFromMapIgnoringUnknownKeys(t *testing.T) {
	// 版本历史向前兼容：未知键忽略，缺失键回落零值。
	// 注意 config 键用 PascalCase（"ChunkSize"）：Map() 对无 tag 的
	// WorkspaceConfig 序列化为 Go 字段名，快照 payload 键即 PascalCase。
	// （brief 初稿误写 snake_case "chunk_size"；Go encoding/json 大小写
	// 不敏感但不剥下划线，snake_case 键会被丢弃——按 Map() 实际输出修正。）
	snap, err := SnapshotFromMap(map[string]any{"name": "N", "config": map[string]any{"ChunkSize": 128}})
	require.NoError(t, err)
	require.Equal(t, "N", snap.Name)
	require.Equal(t, 128, snap.Config.ChunkSize)
	require.Zero(t, snap.Description)
	require.Equal(t, "config", "config") // config 缺失其余字段回落零值
	require.Zero(t, snap.Config.TopK)
}
