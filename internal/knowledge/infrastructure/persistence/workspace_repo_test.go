package persistence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// toJSONB/fromJSONB must round-trip every WorkspaceConfig field. The regression
// this guards: chunking_strategy was absent from jsonbConfig, so a workspace
// read back from rag_workspaces.config always lost its strategy and silently
// fell back to the default — uploads never honored the configured strategy.
func TestWorkspaceConfigJSONBRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		cfg  domain.WorkspaceConfig
	}{
		{
			name: "parent-child strategy",
			cfg: domain.WorkspaceConfig{
				EmbeddingModel:   "text-embedding-3-small",
				ChunkSize:        512,
				ChunkOverlap:     64,
				QueryMode:        "hybrid",
				TopK:             8,
				ChunkingStrategy: "parent_child",
			},
		},
		{
			name: "structure-recursive strategy",
			cfg: domain.WorkspaceConfig{
				EmbeddingModel:   "bge-m3",
				ChunkSize:        1024,
				ChunkOverlap:     128,
				QueryMode:        "vector",
				TopK:             4,
				ChunkingStrategy: "structure_recursive",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := toJSONB(tc.cfg)

			// chunking_strategy must actually land in the JSON payload.
			if !strings.Contains(raw, `"chunking_strategy"`) {
				t.Fatalf("toJSONB dropped chunking_strategy key: %s", raw)
			}

			var jc jsonbConfig
			if err := json.Unmarshal([]byte(raw), &jc); err != nil {
				t.Fatalf("unmarshal toJSONB output: %v", err)
			}
			got := fromJSONB(jc)

			if got != tc.cfg {
				t.Fatalf("round-trip mismatch:\n got  %+v\n want %+v", got, tc.cfg)
			}
		})
	}
}
