package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNewWorkspaceAppliesDefaults(t *testing.T) {
	// 嵌入模型无静态兜底：必须显式配置（empty 由 Validate 拒绝）。
	ws, err := NewWorkspace("kb", "desc", WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}, 1024, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := ws.Config
	if cfg.EmbeddingModel != "text-embedding-v3" {
		t.Errorf("expected explicit embedding model %q preserved, got %q", "text-embedding-v3", cfg.EmbeddingModel)
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
	// 新建 workspace 预填默认评分指令（Q3：前端详情页回填即显示可编辑推荐文本）。
	if cfg.RerankScoringInstructions != DefaultRerankScoringInstructions {
		t.Errorf("expected rerank scoring instructions prefilled, got %q", cfg.RerankScoringInstructions)
	}
	if cfg.JudgeScoringInstructions != DefaultJudgeScoringInstructions {
		t.Errorf("expected judge scoring instructions prefilled, got %q", cfg.JudgeScoringInstructions)
	}
}

func TestNewWorkspaceKeepsProvidedValues(t *testing.T) {
	cfg := WorkspaceConfig{
		EmbeddingModel:            "embedding-3",
		ChunkSize:                 256,
		ChunkOverlap:              32,
		QueryMode:                 "vector",
		TopK:                      3,
		ChunkingStrategy:          ChunkingStrategySemantic,
		RerankScoringInstructions: "自定义重排指令",
		JudgeScoringInstructions:  "自定义判断指令",
	}
	ws, err := NewWorkspace("kb", "", cfg, 1024, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 显式提供的指令值必须原样保留（不被 applyDefaults 的默认文本覆盖）。
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
		// 注意：embedding model 目录存在性由 application 层校验（port.ModelExists）；
		// domain Validate 校验空值（必填）与结构（query/chunking/rerank/threshold）。
		{"unsupported query mode", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", QueryMode: "fuzzy", ChunkingStrategy: "recursive"}, ErrInvalidQueryMode},
		{"unsupported chunking strategy", WorkspaceConfig{EmbeddingModel: "text-embedding-v3", QueryMode: "hybrid", ChunkingStrategy: "weird"}, ErrInvalidChunkingStrategy},
		{"empty embedding model after explicit invalid keeps structural priority", WorkspaceConfig{EmbeddingModel: "", QueryMode: "bad", ChunkingStrategy: "recursive"}, ErrInvalidQueryMode},
		{"empty embedding model rejected (no static default)", WorkspaceConfig{EmbeddingModel: "", QueryMode: "hybrid", ChunkingStrategy: "recursive"}, ErrEmbeddingModelRequired},
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
	valid := WorkspaceConfig{EmbeddingModel: "embedding-3", QueryMode: "graph", TopK: 5, ChunkingStrategy: ChunkingStrategyStructureRecursive}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid config to pass, got %v", err)
	}
	for _, tc := range []struct {
		name string
		mut  func(*WorkspaceConfig)
		want error
	}{
		{"bad mode", func(c *WorkspaceConfig) { c.QueryMode = "x" }, ErrInvalidQueryMode},
		{"bad strategy", func(c *WorkspaceConfig) { c.ChunkingStrategy = "x" }, ErrInvalidChunkingStrategy},
		// 空值由 domain 校验（必填）；未知模型名由 application 目录校验（domain 不做 I/O）。
		{"empty embedding model", func(c *WorkspaceConfig) { c.EmbeddingModel = "" }, ErrEmbeddingModelRequired},
		{"external provider without model", func(c *WorkspaceConfig) { c.Reranking = "cohere" }, ErrInvalidRerankIdentity},
		{"external provider with model passes", func(c *WorkspaceConfig) { c.Reranking = "unknown:model" }, nil},
		{"threshold above range", func(c *WorkspaceConfig) { c.ScoreThreshold = 1.5 }, ErrInvalidScoreThreshold},
		{"threshold below range", func(c *WorkspaceConfig) { c.ScoreThreshold = -0.1 }, ErrInvalidScoreThreshold},
		{"topk above range", func(c *WorkspaceConfig) { c.TopK = 21 }, ErrInvalidTopK},
		{"topk zero rejected", func(c *WorkspaceConfig) { c.TopK = 0 }, ErrInvalidTopK},
		{"rerank topk above range", func(c *WorkspaceConfig) { c.RerankTopK = 21 }, ErrInvalidRerankTopK},
		{"rerank topk negative", func(c *WorkspaceConfig) { c.RerankTopK = -1 }, ErrInvalidRerankTopK},
		// 评分指令超上限（2001 > Max*ScoringInstructionsRunes=2000）拒绝；空串合法
		// （存量 JSONB 无键读回空 → 运行时回落内置 prompt）。
		{"rerank instructions over max", func(c *WorkspaceConfig) { c.RerankScoringInstructions = strings.Repeat("长", 2001) }, ErrInvalidScoringInstructions},
		{"judge instructions over max", func(c *WorkspaceConfig) { c.JudgeScoringInstructions = strings.Repeat("长", 2001) }, ErrInvalidScoringInstructions},
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
	// 除边界回填外，applyDefaults 固定预填两条默认评分指令；预填语义由
	// TestNewWorkspaceAppliesDefaults 专项守护，此处补上以免比较失败遮蔽边界断言。
	withDefaultInstructions := func(c WorkspaceConfig) WorkspaceConfig {
		c.RerankScoringInstructions = DefaultRerankScoringInstructions
		c.JudgeScoringInstructions = DefaultJudgeScoringInstructions
		return c
	}
	cases := []struct {
		name string
		in   WorkspaceConfig
		want WorkspaceConfig
	}{
		// 嵌入模型无静态兜底：applyDefaults 对空模型原样留空，由 Validate 拒绝。
		{"zero chunk size defaults", WorkspaceConfig{ChunkSize: 0, ChunkOverlap: 0, TopK: 0}, withDefaultInstructions(WorkspaceConfig{QueryMode: DefaultQueryMode, ChunkSize: 777, ChunkOverlap: DefaultChunkOverlap, TopK: 9, ChunkingStrategy: DefaultChunkingStrategy})},
		{"negative chunk size defaults", WorkspaceConfig{ChunkSize: -1, ChunkOverlap: -5, TopK: -2}, withDefaultInstructions(WorkspaceConfig{QueryMode: DefaultQueryMode, ChunkSize: 777, ChunkOverlap: DefaultChunkOverlap, TopK: 9, ChunkingStrategy: DefaultChunkingStrategy})},
		{"one chunk size defaults", WorkspaceConfig{ChunkSize: 1, ChunkOverlap: 1, TopK: 1}, withDefaultInstructions(WorkspaceConfig{QueryMode: DefaultQueryMode, ChunkSize: 1, ChunkOverlap: 1, TopK: 1, ChunkingStrategy: DefaultChunkingStrategy})},
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
		{"external provider with model applies", WorkspaceConfig{Reranking: "unknown:model"}, nil, func() WorkspaceConfig { c := base; c.Reranking = "unknown:model"; return c }()},
		{"score threshold applied", WorkspaceConfig{ScoreThreshold: 0.5}, nil, func() WorkspaceConfig { c := base; c.ScoreThreshold = 0.5; return c }()},
		{"score threshold above range", WorkspaceConfig{ScoreThreshold: 1.5}, ErrInvalidScoreThreshold, base},
		{"rerank topk applied", WorkspaceConfig{RerankTopK: 3}, nil, func() WorkspaceConfig { c := base; c.RerankTopK = 3; return c }()},
		{"topk above range", WorkspaceConfig{TopK: 21}, ErrInvalidTopK, base},
		{"rerank topk above range", WorkspaceConfig{RerankTopK: 21}, ErrInvalidRerankTopK, base},
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

	// 哨兵显式 0：partial 零值=未传（0 无法表达重置意图），handler PATCH
	// 整体替换把用户填的 0 编码为负哨兵，domain 必须转回 0——否则
	// score_threshold 设置后永远关不掉。
	t.Run("score threshold reset via sentinel", func(t *testing.T) {
		withThreshold := base
		withThreshold.ScoreThreshold = 0.5
		got, err := withThreshold.MergeUpdate(WorkspaceConfig{ScoreThreshold: ScoreThresholdResetSentinel})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ScoreThreshold != 0 {
			t.Errorf("expected threshold reset to 0, got %v", got.ScoreThreshold)
		}
	})
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
		ErrChunkLimitExceeded, ErrIngestQueueFull,
		ErrInvalidEmbeddingModel, ErrEmbeddingModelRequired,
	} {
		if err == nil {
			t.Errorf("sentinel error is nil")
		}
	}
}

func TestWorkspaceConfigValidateRerankModel(t *testing.T) {
	base := WorkspaceConfig{
		EmbeddingModel:   "text-embedding-v3",
		QueryMode:        "hybrid",
		TopK:             5,
		ChunkingStrategy: "recursive",
	}
	cases := []struct {
		name    string
		cfg     WorkspaceConfig
		wantErr error
	}{
		{
			name: "builtin rerank 无模型拒绝",
			cfg: func() WorkspaceConfig {
				c := base
				c.Reranking = "builtin-score-v1"
				return c
			}(),
			wantErr: ErrRerankModelRequired,
		},
		{
			name: "builtin rerank 有模型通过",
			cfg: func() WorkspaceConfig {
				c := base
				c.Reranking = "builtin-score-v1"
				c.RerankModel = "qwen-turbo"
				return c
			}(),
		},
		{
			name: "外部 rerank 不需要 rerank_model",
			cfg: func() WorkspaceConfig {
				c := base
				c.Reranking = "cohere:rerank-multilingual-v3.0"
				return c
			}(),
		},
		{
			name: "judge_model 空可通过（门关闭）",
			cfg:  base,
		},
		{
			name: "embedding 缺失优先于 rerank 缺失",
			cfg: func() WorkspaceConfig {
				c := base
				c.EmbeddingModel = ""
				c.Reranking = "builtin-score-v1"
				return c
			}(),
			wantErr: ErrEmbeddingModelRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestMergeUpdateRerankModelSentinels(t *testing.T) {
	base := WorkspaceConfig{
		EmbeddingModel:   "text-embedding-v3",
		QueryMode:        "hybrid",
		ChunkingStrategy: "recursive",
		Reranking:        "builtin-score-v1",
		RerankModel:      "qwen-turbo",
		JudgeModel:       "qwen-plus",
	}
	t.Run("显式清空 judge_model（sentinel）", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{JudgeModel: JudgeModelResetSentinel})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.JudgeModel != "" {
			t.Fatalf("JudgeModel = %q, want cleared", got.JudgeModel)
		}
		if got.RerankModel != "qwen-turbo" {
			t.Fatalf("RerankModel must be untouched, got %q", got.RerankModel)
		}
	})
	t.Run("零值 judge_model 不覆盖（partial 语义）", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.JudgeModel != "qwen-plus" {
			t.Fatalf("JudgeModel = %q, want preserved", got.JudgeModel)
		}
	})
	t.Run("显式设置 rerank_model 覆盖", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{RerankModel: "qwen-max"})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.RerankModel != "qwen-max" {
			t.Fatalf("RerankModel = %q, want qwen-max", got.RerankModel)
		}
	})
	t.Run("显式清空 reranking（sentinel）", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{Reranking: RerankingResetSentinel})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.Reranking != "" {
			t.Fatalf("Reranking = %q, want cleared", got.Reranking)
		}
	})
}

// TestMergeUpdateScoringInstructionsSentinels 守护两条评分指令的 partial 合并语义：
// 零值=未传保留 / 非空覆盖 / 哨兵显式清空（TextArea 清空 → handler 编码 NUL 哨兵）/
// 超限 fail-closed（Update 路径不经过 Validate，merge 内做长度校验）。
func TestMergeUpdateScoringInstructionsSentinels(t *testing.T) {
	base := WorkspaceConfig{
		EmbeddingModel:            "text-embedding-v3",
		QueryMode:                 "hybrid",
		ChunkingStrategy:          "recursive",
		RerankScoringInstructions: "现有重排指令",
		JudgeScoringInstructions:  "现有判断指令",
	}
	t.Run("零值不覆盖（partial 语义）", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.RerankScoringInstructions != "现有重排指令" || got.JudgeScoringInstructions != "现有判断指令" {
			t.Fatalf("zero partial must preserve base, got %+v", got)
		}
	})
	t.Run("非空覆盖", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{RerankScoringInstructions: "新重排", JudgeScoringInstructions: "新判断"})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.RerankScoringInstructions != "新重排" || got.JudgeScoringInstructions != "新判断" {
			t.Fatalf("non-empty partial must override, got %+v", got)
		}
	})
	t.Run("哨兵显式清空", func(t *testing.T) {
		got, err := base.MergeUpdate(WorkspaceConfig{
			RerankScoringInstructions: RerankScoringInstructionsResetSentinel,
			JudgeScoringInstructions:  JudgeScoringInstructionsResetSentinel,
		})
		if err != nil {
			t.Fatalf("MergeUpdate() error = %v", err)
		}
		if got.RerankScoringInstructions != "" || got.JudgeScoringInstructions != "" {
			t.Fatalf("sentinels must clear both, got %+v", got)
		}
	})
	t.Run("超限拒绝", func(t *testing.T) {
		over := strings.Repeat("长", 2001)
		_, err := base.MergeUpdate(WorkspaceConfig{RerankScoringInstructions: over})
		if !errors.Is(err, ErrInvalidScoringInstructions) {
			t.Fatalf("over-max rerank instructions must be rejected, got %v", err)
		}
		_, err = base.MergeUpdate(WorkspaceConfig{JudgeScoringInstructions: over})
		if !errors.Is(err, ErrInvalidScoringInstructions) {
			t.Fatalf("over-max judge instructions must be rejected, got %v", err)
		}
	})
}
