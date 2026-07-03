package domain

import (
	"errors"
	"time"
)

// Sentinel errors raised by Workspace domain methods. Application-level
// names mirror these so the HTTP error mapping table can route both layers.
var (
	ErrInvalidEmbeddingModel   = errors.New("unsupported embedding model")
	ErrInvalidQueryMode        = errors.New("invalid query_mode")
	ErrEmbeddingModelImmutable = errors.New("embedding_model is immutable after creation")
	ErrChunkSizeImmutable      = errors.New("chunk_size is immutable after creation")
	ErrChunkOverlapImmutable   = errors.New("chunk_overlap is immutable after creation")
)

const (
	DefaultEmbeddingModel = "text-embedding-v3"
	DefaultQueryMode      = "hybrid"
	DefaultChunkSize      = 512
	DefaultChunkOverlap   = 64
	DefaultTopK           = 5
)

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

// Workspace is a knowledge RAG workspace owned by a tenant.
type Workspace struct {
	ID          string
	Name        string
	Description string
	Config      WorkspaceConfig
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// WorkspaceConfig is the per-workspace RAG configuration persisted as JSONB.
type WorkspaceConfig struct {
	EmbeddingModel string
	ChunkSize      int
	ChunkOverlap   int
	QueryMode      string
	TopK           int
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

// Validate checks that EmbeddingModel and QueryMode fall within the allowed sets.
func (c WorkspaceConfig) Validate() error {
	if !AllowedEmbeddingModels[c.EmbeddingModel] {
		return ErrInvalidEmbeddingModel
	}
	if !AllowedQueryModes[c.QueryMode] {
		return ErrInvalidQueryMode
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
	return c
}

// MergeUpdate returns the result of applying a partial update to the current
// config. It enforces immutability of embedding_model / chunk_size / chunk_overlap
// and validates the resulting query_mode.
func (c WorkspaceConfig) MergeUpdate(partial WorkspaceConfig) (WorkspaceConfig, error) {
	out := c
	if partial.EmbeddingModel != "" && partial.EmbeddingModel != c.EmbeddingModel {
		return c, ErrEmbeddingModelImmutable
	}
	if partial.ChunkSize > 0 && partial.ChunkSize != c.ChunkSize {
		return c, ErrChunkSizeImmutable
	}
	if partial.ChunkOverlap > 0 && partial.ChunkOverlap != c.ChunkOverlap {
		return c, ErrChunkOverlapImmutable
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
