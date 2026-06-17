package dto

import (
	"mime/multipart"
	"time"
)

// WorkspaceConfig is the per-workspace RAG configuration carried over the wire.
type WorkspaceConfig struct {
	EmbeddingModel string `json:"embedding_model"`
	ChunkSize      int    `json:"chunk_size"`
	ChunkOverlap   int    `json:"chunk_overlap"`
	QueryMode      string `json:"query_mode"`
	TopK           int    `json:"top_k"`
}

// UploadDocumentRequest is bound from POST /knowledge/ingest multipart form.
type UploadDocumentRequest struct {
	Workspace string                `form:"workspace" binding:"required"`
	File      *multipart.FileHeader `form:"file" binding:"required"`
}

// QueryRequest is bound from POST /knowledge/query JSON body.
type QueryRequest struct {
	Question  string `json:"question" binding:"required"`
	Workspace string `json:"workspace" binding:"required"`
	Mode      string `json:"mode" binding:"required,oneof=vector graph hybrid"`
	TopK      int    `json:"topK"`
}

// CreateWorkspaceRequest is bound from POST /knowledge/workspaces JSON body.
type CreateWorkspaceRequest struct {
	Name        string          `json:"name" binding:"required"`
	Description string          `json:"description"`
	Config      WorkspaceConfig `json:"config"`
}

// UpdateWorkspaceRequest is bound from PATCH /knowledge/workspaces/:name JSON body.
type UpdateWorkspaceRequest struct {
	Description *string          `json:"description"`
	Config      *WorkspaceConfig `json:"config"`
}

// IngestDocumentRequest is bound from JSON ingest payloads (raw byte upload).
type IngestDocumentRequest struct {
	Workspace    string `json:"workspace" binding:"required"`
	DocumentData []byte `json:"document_data" binding:"required"`
	FileName     string `json:"filename" binding:"required"`
	DocumentID   string `json:"document_id" binding:"required"`
}

// WorkspaceListItem is a row in GET /knowledge/workspaces response.
type WorkspaceListItem struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      WorkspaceConfig `json:"config"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
