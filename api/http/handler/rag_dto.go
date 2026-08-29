// Package handler — rag_dto.go.
//
// Wire DTOs for knowledge workspace version history and rollback endpoints.
// Field shapes are frozen by api/http/contract_test.go +
// testdata/contracts/*.golden.json; keep in sync with agent_dto.go's
// AgentVersionResponse (front-end shares the VersionHistory component).
package handler

import (
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
)

// WorkspaceVersionResponse mirrors knowledge.WorkspaceVersionDTO for the wire.
// Field shapes are frozen by contract tests; do not rename.
type WorkspaceVersionResponse struct {
	ID            string         `json:"id"`
	VersionNo     int            `json:"versionNo"`
	Status        string         `json:"status"`
	Source        string         `json:"source"`
	ContentHash   string         `json:"contentHash"`
	CreatedBy     string         `json:"createdBy"`
	CreatedByName string         `json:"createdByName"`
	CreatedAt     string         `json:"createdAt"`
	PublishedAt   string         `json:"publishedAt"`
	IsCurrent     bool           `json:"isCurrent"`
	SafeSummary   map[string]any `json:"safeSummary"`
}

// WorkspaceVersionsResponse wraps the version history list (newest first),
// matching the agent versions response shape for frontend symmetry.
type WorkspaceVersionsResponse struct {
	Versions []WorkspaceVersionResponse `json:"versions"`
}

// RollbackWorkspaceRequest carries the target historical version to restore.
type RollbackWorkspaceRequest struct {
	VersionID string `json:"versionId" binding:"required"`
}

// workspaceVersionToResponse maps the service-side DTO to the wire shape.
func workspaceVersionToResponse(v knowledge.WorkspaceVersionDTO) WorkspaceVersionResponse {
	return WorkspaceVersionResponse{
		ID:            v.ID,
		VersionNo:     v.VersionNo,
		Status:        v.Status,
		Source:        v.Source,
		ContentHash:   v.ContentHash,
		CreatedBy:     v.CreatedBy,
		CreatedByName: v.CreatedByName,
		CreatedAt:     v.CreatedAt,
		PublishedAt:   v.PublishedAt,
		IsCurrent:     v.IsCurrent,
		SafeSummary:   v.SafeSummary,
	}
}
