package domain

import (
	"errors"
	"strings"
	"time"
)

// Sentinel errors raised by Workspace domain methods. Application-level
// names mirror these so the HTTP error mapping table can route both layers.
var (
	ErrInvalidEmbeddingModel     = errors.New("unsupported embedding model")
	ErrInvalidQueryMode          = errors.New("invalid query_mode")
	ErrInvalidChunkingStrategy   = errors.New("invalid chunking_strategy")
	ErrInvalidRerankIdentity     = errors.New("invalid reranking identity")
	ErrInvalidScoreThreshold     = errors.New("score_threshold must be within [0, 1]")
	ErrEmbeddingModelImmutable   = errors.New("embedding_model is immutable after creation")
	ErrChunkSizeImmutable        = errors.New("chunk_size is immutable after creation")
	ErrChunkOverlapImmutable     = errors.New("chunk_overlap is immutable after creation")
	ErrChunkingStrategyImmutable = errors.New("chunking_strategy is immutable after creation")
)

const (
	DefaultEmbeddingModel   = "text-embedding-v3"
	DefaultQueryMode        = "hybrid"
	DefaultChunkSize        = 512
	DefaultChunkOverlap     = 64
	DefaultTopK             = 5
	DefaultChunkingStrategy = ChunkingStrategyStructureRecursive

	ChunkingStrategyRecursive          = "recursive"
	ChunkingStrategyStructureRecursive = "structure_recursive"
	ChunkingStrategySemantic           = "semantic"
)

var AllowedChunkingStrategies = map[string]bool{
	ChunkingStrategyRecursive:          true,
	ChunkingStrategyStructureRecursive: true,
	ChunkingStrategySemantic:           true,
}

// AllowedEmbeddingModels enumerates models the system can serve embeddings for.
// Extend here when a new provider is wired up; service / handler must not redefine.
var AllowedEmbeddingModels = map[string]bool{
	"text-embedding-v3": true,
	"embedding-3":       true,
}

// AllowedQueryModes enumerates RAG query strategies recognised by RAGService.
var AllowedQueryModes = map[string]bool{
	"vector": true,
	"graph":  true,
	"hybrid": true,
}

// AllowedRerankIdentities enumerates rerank strategies recognised by
// RAGService: "" (none), the local builtin scorer, and external providers in
// "provider:model" form. Extend here when a new reranker is wired up.
var AllowedRerankIdentities = map[string]bool{
	"":                 true,
	"builtin-score-v1": true,
	"cohere":           true,
}

// SplitRerankIdentity splits "provider:model" style rerank identities;
// bare strings are provider-only (e.g. builtin-score-v1).
func SplitRerankIdentity(identity string) (provider, model string) {
	if i := strings.Index(identity, ":"); i >= 0 {
		return identity[:i], identity[i+1:]
	}
	return identity, ""
}

// Workspace is a knowledge RAG workspace owned by a tenant.
type Workspace struct {
	ID             string
	Name           string
	Description    string
	Config         WorkspaceConfig
	SystemKey      string `json:"-"`
	ManagementMode string `json:"management_mode"`
	// CreatedBy is the user who created the workspace ("" for historical/platform rows).
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WorkspaceConfig is the per-workspace RAG configuration persisted as JSONB.
type WorkspaceConfig struct {
	EmbeddingModel   string
	ChunkSize        int
	ChunkOverlap     int
	QueryMode        string
	TopK             int
	ChunkingStrategy string
	// Reranking is the rerank strategy identity: "" (none),
	// "builtin-score-v1" (local score desc), or "provider:model" for an
	// external reranker. ScoreThreshold keeps only results with sim >=
	// threshold (0 disables); RerankTopK is the final count after external
	// reranking (0 uses TopK).
	Reranking      string
	ScoreThreshold float32
	RerankTopK     int
}

// NewWorkspace constructs a Workspace, applying defaults to cfg and validating it.
// Callers receive ErrInvalidEmbeddingModel / ErrInvalidQueryMode on bad input.
func NewWorkspace(name, description string, cfg WorkspaceConfig, defaultChunkSize, defaultTopK int) (*Workspace, error) {
	cfg = applyDefaults(cfg, defaultChunkSize, defaultTopK)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Workspace{
		Name:        name,
		Description: description,
		Config:      cfg,
	}, nil
}

// Validate checks that EmbeddingModel, QueryMode, ChunkingStrategy, and the
// rerank settings fall within the allowed sets.
func (c WorkspaceConfig) Validate() error {
	if !AllowedEmbeddingModels[c.EmbeddingModel] {
		return ErrInvalidEmbeddingModel
	}
	if !AllowedQueryModes[c.QueryMode] {
		return ErrInvalidQueryMode
	}
	if !AllowedChunkingStrategies[c.ChunkingStrategy] {
		return ErrInvalidChunkingStrategy
	}
	provider, model := SplitRerankIdentity(c.Reranking)
	if !AllowedRerankIdentities[provider] || (provider == "cohere" && model == "") {
		return ErrInvalidRerankIdentity
	}
	if c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		return ErrInvalidScoreThreshold
	}
	return nil
}

func applyDefaults(c WorkspaceConfig, defaultChunkSize, defaultTopK int) WorkspaceConfig {
	if c.EmbeddingModel == "" {
		c.EmbeddingModel = DefaultEmbeddingModel
	}
	if c.QueryMode == "" {
		c.QueryMode = DefaultQueryMode
	}
	if c.ChunkSize <= 0 {
		c.ChunkSize = defaultChunkSize
	}
	if c.ChunkOverlap <= 0 {
		c.ChunkOverlap = DefaultChunkOverlap
	}
	if c.TopK <= 0 {
		c.TopK = defaultTopK
	}
	if c.ChunkingStrategy == "" {
		c.ChunkingStrategy = DefaultChunkingStrategy
	}
	return c
}

// MergeUpdate returns the result of applying a partial update to the current
// config. It enforces immutability of embedding_model / chunk_size / chunk_overlap
// and validates the resulting query_mode and rerank settings.
func (c WorkspaceConfig) MergeUpdate(partial WorkspaceConfig) (WorkspaceConfig, error) {
	out := c
	if err := c.applyImmutableSettings(partial); err != nil {
		return c, err
	}
	if partial.QueryMode != "" {
		if !AllowedQueryModes[partial.QueryMode] {
			return c, ErrInvalidQueryMode
		}
		out.QueryMode = partial.QueryMode
	}
	if partial.TopK > 0 {
		out.TopK = partial.TopK
	}
	merged, err := out.applyRerankSettings(partial)
	if err != nil {
		return c, err
	}
	return merged, nil
}

// applyImmutableSettings rejects updates to fields that are fixed after
// workspace creation.
func (c WorkspaceConfig) applyImmutableSettings(partial WorkspaceConfig) error {
	if partial.EmbeddingModel != "" && partial.EmbeddingModel != c.EmbeddingModel {
		return ErrEmbeddingModelImmutable
	}
	if partial.ChunkSize > 0 && partial.ChunkSize != c.ChunkSize {
		return ErrChunkSizeImmutable
	}
	if partial.ChunkOverlap > 0 && partial.ChunkOverlap != c.ChunkOverlap {
		return ErrChunkOverlapImmutable
	}
	if partial.ChunkingStrategy != "" && partial.ChunkingStrategy != c.ChunkingStrategy {
		return ErrChunkingStrategyImmutable
	}
	return nil
}

// applyRerankSettings merges the rerank fields, validating the identity and
// score threshold before applying.
func (c WorkspaceConfig) applyRerankSettings(partial WorkspaceConfig) (WorkspaceConfig, error) {
	out := c
	if partial.Reranking != "" {
		provider, model := SplitRerankIdentity(partial.Reranking)
		if !AllowedRerankIdentities[provider] || (provider == "cohere" && model == "") {
			return c, ErrInvalidRerankIdentity
		}
		out.Reranking = partial.Reranking
	}
	if partial.ScoreThreshold > 0 {
		if partial.ScoreThreshold > 1 {
			return c, ErrInvalidScoreThreshold
		}
		out.ScoreThreshold = partial.ScoreThreshold
	}
	if partial.RerankTopK > 0 {
		out.RerankTopK = partial.RerankTopK
	}
	return out, nil
}

// Rename mutates Name; reserved for future when name editing is allowed.
func (w *Workspace) Rename(name string) {
	w.Name = name
}

// UpdateDescription mutates Description on the aggregate.
func (w *Workspace) UpdateDescription(desc string) {
	w.Description = desc
}

// UpdateConfig replaces the workspace config with the supplied (already-validated) cfg.
func (w *Workspace) UpdateConfig(cfg WorkspaceConfig) {
	w.Config = cfg
}
