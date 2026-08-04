package domain

import (
	"errors"
	"testing"
)

func TestNewWorkspaceAppliesDefaults(t *testing.T) {
	ws, err := NewWorkspace("kb", "desc", WorkspaceConfig{}, 1024, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := ws.Config
	if cfg.EmbeddingModel != DefaultEmbeddingModel {
		t.Errorf("expected default embedding model %q, got %q", DefaultEmbeddingModel, cfg.EmbeddingModel)
	}
	if cfg.QueryMode != DefaultQueryMode {
		t.Errorf("expected default query mode %q, got %q", DefaultQueryMode, cfg.QueryMode)
	}
	if cfg.ChunkSize != 1024 {
		t.Errorf("expected caller chunk size 1024, got %d", cfg.ChunkSize)
	}
	if cfg.ChunkOverlap != DefaultChunkOverlap {
		t.Errorf("expected default overlap %d, got %d", DefaultChunkOverlap, cfg.ChunkOverlap)
	}
	if cfg.TopK != 10 {
		t.Errorf("expected caller topK 10, got %d", cfg.TopK)
	}
	if cfg.ChunkingStrategy != DefaultChunkingStrategy {
		t.Errorf("expected default chunking %q, got %q", DefaultChunkingStrategy, cfg.ChunkingStrategy)
	}
}

func TestNewWorkspaceKeepsProvidedValues(t *testing.T) {
	cfg := WorkspaceConfig{
		EmbeddingModel:   "embedding-3",
		ChunkSize:        256,
		ChunkOverlap:     32,
		QueryMode:        "vector",
		TopK:             3,
		ChunkingStrategy: ChunkingStrategySemantic,
	}
	ws, err := NewWorkspace("kb", "", cfg, 1024, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws.Config != cfg {
		t.Errorf("expected provided config preserved, got %+v", ws.Config)
	}
	if ws.Name != "kb" || ws.Description != "" {
		t.Errorf("name/description not preserved: %+v", ws)
	}
}

func TestNewWorkspaceRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  WorkspaceConfig
		want error
	}{
		{"unsupported embedding model", WorkspaceConfig{EmbeddingModel: "nope", QueryMode: "hybrid", ChunkingStrategy: "recursive"}, ErrInvalidEmbeddingModel},
		{"unsupported query mode", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", QueryMode: "fuzzy", ChunkingStrategy: "recursive"}, ErrInvalidQueryMode},
		{"unsupported chunking strategy", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", QueryMode: "hybrid", ChunkingStrategy: "weird"}, ErrInvalidChunkingStrategy},
		{"empty embedding model cannot default after explicit invalid", WorkspaceConfig{EmbeddingModel: "", QueryMode: "bad", ChunkingStrategy: "recursive"}, ErrInvalidQueryMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewWorkspace("kb", "", tc.cfg, 512, 5)
			if !errors.Is(err, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestWorkspaceConfigValidate(t *testing.T) {
	valid := WorkspaceConfig{EmbeddingModel: "embedding-3", QueryMode: "graph", ChunkingStrategy: ChunkingStrategyStructureRecursive}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid config to pass, got %v", err)
	}
	for _, tc := range []struct {
		name string
		mut  func(*WorkspaceConfig)
		want error
	}{
		{"bad model", func(c *WorkspaceConfig) { c.EmbeddingModel = "x" }, ErrInvalidEmbeddingModel},
		{"bad mode", func(c *WorkspaceConfig) { c.QueryMode = "x" }, ErrInvalidQueryMode},
		{"bad strategy", func(c *WorkspaceConfig) { c.ChunkingStrategy = "x" }, ErrInvalidChunkingStrategy},
		{"empty model", func(c *WorkspaceConfig) { c.EmbeddingModel = "" }, ErrInvalidEmbeddingModel},
		{"external provider without model", func(c *WorkspaceConfig) { c.Reranking = "cohere" }, ErrInvalidRerankIdentity},
		{"unknown rerank provider", func(c *WorkspaceConfig) { c.Reranking = "unknown:model" }, ErrInvalidRerankIdentity},
		{"threshold above range", func(c *WorkspaceConfig) { c.ScoreThreshold = 1.5 }, ErrInvalidScoreThreshold},
		{"threshold below range", func(c *WorkspaceConfig) { c.ScoreThreshold = -0.1 }, ErrInvalidScoreThreshold},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mut(&cfg)
			if !errors.Is(cfg.Validate(), tc.want) {
				t.Errorf("expected %v, got %v", tc.want, cfg.Validate())
			}
		})
	}
}

func TestApplyDefaultsBoundaries(t *testing.T) {
	cases := []struct {
		name string
		in   WorkspaceConfig
		want WorkspaceConfig
	}{
		{"zero chunk size defaults", WorkspaceConfig{ChunkSize: 0, ChunkOverlap: 0, TopK: 0}, WorkspaceConfig{EmbeddingModel: DefaultEmbeddingModel, QueryMode: DefaultQueryMode, ChunkSize: 777, ChunkOverlap: DefaultChunkOverlap, TopK: 9, ChunkingStrategy: DefaultChunkingStrategy}},
		{"negative chunk size defaults", WorkspaceConfig{ChunkSize: -1, ChunkOverlap: -5, TopK: -2}, WorkspaceConfig{EmbeddingModel: DefaultEmbeddingModel, QueryMode: DefaultQueryMode, ChunkSize: 777, ChunkOverlap: DefaultChunkOverlap, TopK: 9, ChunkingStrategy: DefaultChunkingStrategy}},
		{"one chunk size defaults", WorkspaceConfig{ChunkSize: 1, ChunkOverlap: 1, TopK: 1}, WorkspaceConfig{EmbeddingModel: DefaultEmbeddingModel, QueryMode: DefaultQueryMode, ChunkSize: 1, ChunkOverlap: 1, TopK: 1, ChunkingStrategy: DefaultChunkingStrategy}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyDefaults(tc.in, 777, 9)
			if got != tc.want {
				t.Errorf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestMergeUpdate(t *testing.T) {
	base := WorkspaceConfig{EmbeddingModel: "text-embedding-v3", ChunkSize: 512, ChunkOverlap: 64, QueryMode: "hybrid", TopK: 5, ChunkingStrategy: "recursive"}

	cases := []struct {
		name    string
		partial WorkspaceConfig
		wantErr error
		want    WorkspaceConfig
	}{
		{"empty partial keeps base", WorkspaceConfig{}, nil, base},
		{"query mode change", WorkspaceConfig{QueryMode: "vector"}, nil, func() WorkspaceConfig { c := base; c.QueryMode = "vector"; return c }()},
		{"topK change", WorkspaceConfig{TopK: 20}, nil, func() WorkspaceConfig { c := base; c.TopK = 20; return c }()},
		{"same values no-op", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", ChunkSize: 512, ChunkOverlap: 64}, nil, base},
		{"embedding model immutable", WorkspaceConfig{EmbeddingModel: "embedding-3"}, ErrEmbeddingModelImmutable, base},
		{"chunk size immutable", WorkspaceConfig{ChunkSize: 100}, ErrChunkSizeImmutable, base},
		{"chunk overlap immutable", WorkspaceConfig{ChunkOverlap: 100}, ErrChunkOverlapImmutable, base},
		{"chunking strategy immutable", WorkspaceConfig{ChunkingStrategy: "semantic"}, ErrChunkingStrategyImmutable, base},
		{"invalid query mode", WorkspaceConfig{QueryMode: "bad"}, ErrInvalidQueryMode, base},
		{"external rerank identity", WorkspaceConfig{Reranking: "cohere:rerank-v3.0"}, nil, func() WorkspaceConfig { c := base; c.Reranking = "cohere:rerank-v3.0"; return c }()},
		{"builtin rerank identity", WorkspaceConfig{Reranking: "builtin-score-v1"}, nil, func() WorkspaceConfig { c := base; c.Reranking = "builtin-score-v1"; return c }()},
		{"external rerank without model", WorkspaceConfig{Reranking: "cohere"}, ErrInvalidRerankIdentity, base},
		{"unknown rerank provider", WorkspaceConfig{Reranking: "unknown:model"}, ErrInvalidRerankIdentity, base},
		{"score threshold applied", WorkspaceConfig{ScoreThreshold: 0.5}, nil, func() WorkspaceConfig { c := base; c.ScoreThreshold = 0.5; return c }()},
		{"score threshold above range", WorkspaceConfig{ScoreThreshold: 1.5}, ErrInvalidScoreThreshold, base},
		{"rerank topk applied", WorkspaceConfig{RerankTopK: 3}, nil, func() WorkspaceConfig { c := base; c.RerankTopK = 3; return c }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := base.MergeUpdate(tc.partial)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if err == nil && got != tc.want {
				t.Errorf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestWorkspaceMutations(t *testing.T) {
	ws, err := NewWorkspace("a", "d", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", QueryMode: "hybrid", ChunkingStrategy: "recursive"}, 512, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws.Rename("b")
	if ws.Name != "b" {
		t.Errorf("Rename failed, got %q", ws.Name)
	}
	ws.UpdateDescription("d2")
	if ws.Description != "d2" {
		t.Errorf("UpdateDescription failed, got %q", ws.Description)
	}
	// 极端情况：覆盖为全零配置不触发校验（UpdateConfig 不校验）。
	ws.UpdateConfig(WorkspaceConfig{})
	if ws.Config != (WorkspaceConfig{}) {
		t.Errorf("UpdateConfig failed, got %+v", ws.Config)
	}
}

func TestSentinelErrorsNonNil(t *testing.T) {
	// 防御：哨兵错误必须全部可被 errors.Is 匹配且非 nil。
	for _, err := range []error{
		ErrWorkspaceNotFound, ErrWorkspaceConflict, ErrWorkspaceLinked,
		ErrDuplicateDocument, ErrDocumentNotFound, ErrDocumentProcessing,
		ErrChunkLimitExceeded, ErrIngestQueueFull, ErrPlatformManagedWorkspace,
	} {
		if err == nil {
			t.Errorf("sentinel error is nil")
		}
	}
}
