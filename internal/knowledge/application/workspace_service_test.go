package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type deleteWorkspaceRepo struct{ workspace *domain.Workspace }

func (r *deleteWorkspaceRepo) Create(context.Context, string, *domain.Workspace) error { return nil }
func (r *deleteWorkspaceRepo) List(context.Context, string) ([]*domain.Workspace, error) {
	return []*domain.Workspace{r.workspace}, nil
}
func (r *deleteWorkspaceRepo) GetByName(context.Context, string, string) (*domain.Workspace, error) {
	return r.workspace, nil
}
func (r *deleteWorkspaceRepo) UpdateName(context.Context, string, string, string) error { return nil }
func (r *deleteWorkspaceRepo) UpdateDescriptionAndConfig(context.Context, string, string, *string, domain.WorkspaceConfig) error {
	return nil
}
func (r *deleteWorkspaceRepo) Delete(context.Context, string, string) error { return nil }
func (r *deleteWorkspaceRepo) GetConfigForUpload(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return r.workspace.Config, nil
}
func (r *deleteWorkspaceRepo) GetConfigByID(context.Context, string, string) (domain.WorkspaceConfig, error) {
	return r.workspace.Config, nil
}

type deleteDocRepo struct {
	docs       []*domain.Document
	deletedIDs []string
}

func (r *deleteDocRepo) Save(context.Context, string, string, *domain.Document) error { return nil }
func (r *deleteDocRepo) List(context.Context, string, string) ([]*domain.Document, error) {
	return r.docs, nil
}
func (r *deleteDocRepo) Delete(_ context.Context, _, _, docID string) error {
	r.deletedIDs = append(r.deletedIDs, docID)
	return nil
}
func (r *deleteDocRepo) ExistsByHash(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (r *deleteDocRepo) CountByWorkspace(context.Context, string, string) (int, error) {
	return len(r.docs), nil
}
func (r *deleteDocRepo) MarkIngestStarted(context.Context, string, string, int) error   { return nil }
func (r *deleteDocRepo) MarkIngestCompleted(context.Context, string, string, int) error { return nil }
func (r *deleteDocRepo) MarkIngestFailed(context.Context, string, string, string) error { return nil }
func (r *deleteDocRepo) RecoverStuckIngests(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

type deleteVectorStore struct{ deletedDocIDs []string }

func (s *deleteVectorStore) CreateCollectionWithDim(context.Context, string, int) error { return nil }
func (s *deleteVectorStore) DeleteByDocumentIDs(_ context.Context, _ string, docIDs []string) error {
	s.deletedDocIDs = append([]string(nil), docIDs...)
	return nil
}

func TestDeleteDocumentRejectsProcessingDocument(t *testing.T) {
	docs := &deleteDocRepo{docs: []*domain.Document{{ID: "doc-1", IngestStatus: "processing"}}}
	vectors := &deleteVectorStore{}
	service := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{ID: "ws-1", Name: "docs"}}, nil, zap.NewNop())
	service.SetDocRepo(docs)
	service.SetVectorStore(vectors)

	err := service.DeleteDocument(context.Background(), "tenant-1", "docs", "doc-1")
	if !errors.Is(err, domain.ErrDocumentProcessing) {
		t.Fatalf("expected ErrDocumentProcessing, got %v", err)
	}
	if len(docs.deletedIDs) != 0 || len(vectors.deletedDocIDs) != 0 {
		t.Fatal("processing document must not trigger storage cleanup")
	}
}

func TestDeleteDocumentCleansTerminalDocument(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			docs := &deleteDocRepo{docs: []*domain.Document{{ID: "doc-1", IngestStatus: status}}}
			vectors := &deleteVectorStore{}
			service := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{ID: "ws-1", Name: "docs"}}, nil, zap.NewNop())
			service.SetDocRepo(docs)
			service.SetVectorStore(vectors)
			if err := service.DeleteDocument(context.Background(), "tenant-1", "docs", "doc-1"); err != nil {
				t.Fatalf("DeleteDocument() error = %v", err)
			}
			if len(vectors.deletedDocIDs) != 1 || vectors.deletedDocIDs[0] != "doc-1" {
				t.Fatalf("deleted vector IDs = %v", vectors.deletedDocIDs)
			}
			if len(docs.deletedIDs) != 1 || docs.deletedIDs[0] != "doc-1" {
				t.Fatalf("deleted DB IDs = %v", docs.deletedIDs)
			}
		})
	}
}
