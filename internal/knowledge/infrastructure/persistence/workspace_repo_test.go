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

func TestJSONBRoundTripRerankAndJudgeModels(t *testing.T) {
	in := domain.WorkspaceConfig{
		EmbeddingModel:   "text-embedding-v3",
		ChunkSize:        512,
		ChunkOverlap:     64,
		QueryMode:        "hybrid",
		TopK:             5,
		ChunkingStrategy: "recursive",
		Reranking:        "builtin-score-v1",
		RerankModel:      "qwen-turbo",
		JudgeModel:       "qwen-plus",
	}
	var jc jsonbConfig
	_ = json.Unmarshal([]byte(toJSONB(in)), &jc)
	out := fromJSONB(jc)
	if out.RerankModel != "qwen-turbo" || out.JudgeModel != "qwen-plus" {
		t.Fatalf("round-trip lost models: RerankModel=%q JudgeModel=%q", out.RerankModel, out.JudgeModel)
	}
	if !strings.Contains(toJSONB(in), `"rerank_model":"qwen-turbo"`) {
		t.Fatalf("rerank_model key missing/omitempty in JSON: %s", toJSONB(in))
	}
}

func TestJSONBEmptyModelsWriteEmptyKeys(t *testing.T) {
	// 空模型也写入显式键（不带 omitempty）：部署后新行必有键，迁移谓词
	// config->>'rerank_model' IS NULL 只命中部署前旧行。
	cfg := domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}
	if got := toJSONB(cfg); !strings.Contains(got, `"rerank_model":""`) || !strings.Contains(got, `"judge_model":""`) {
		t.Fatalf("empty models must serialize explicit empty keys, got: %s", got)
	}
}

func TestJSONBRoundTripScoringInstructions(t *testing.T) {
	// 非空评分指令必须 round-trip 完整，且落 JSON 键。
	in := domain.WorkspaceConfig{
		EmbeddingModel:            "text-embedding-v3",
		ChunkSize:                 512,
		ChunkOverlap:              64,
		QueryMode:                 "hybrid",
		TopK:                      5,
		ChunkingStrategy:          "recursive",
		RerankScoringInstructions: "按相关性打分，需有区分度",
		JudgeScoringInstructions:  "证据不足判 insufficient",
	}
	var jc jsonbConfig
	_ = json.Unmarshal([]byte(toJSONB(in)), &jc)
	out := fromJSONB(jc)
	if out.RerankScoringInstructions != "按相关性打分，需有区分度" || out.JudgeScoringInstructions != "证据不足判 insufficient" {
		t.Fatalf("round-trip lost instructions: Rerank=%q Judge=%q",
			out.RerankScoringInstructions, out.JudgeScoringInstructions)
	}
	if raw := toJSONB(in); !strings.Contains(raw, `"rerank_scoring_instructions"`) || !strings.Contains(raw, `"judge_scoring_instructions"`) {
		t.Fatalf("non-empty instructions must serialize keys, got: %s", raw)
	}
}

func TestJSONBEmptyInstructionsOmitKeys(t *testing.T) {
	// 空指令带 omitempty 不落键：存量 workspace JSONB 无指令键读回零值 = 内置
	// prompt，无需迁移。空键不得如 rerank_model 般显式写 ""。
	cfg := domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}
	raw := toJSONB(cfg)
	if strings.Contains(raw, "rerank_scoring_instructions") || strings.Contains(raw, "judge_scoring_instructions") {
		t.Fatalf("empty instructions must omit keys, got: %s", raw)
	}
	if got := fromJSONB(jsonbConfig{}).RerankScoringInstructions; got != "" {
		t.Fatalf("missing keys must read back empty (builtin prompt), got %q", got)
	}
}
