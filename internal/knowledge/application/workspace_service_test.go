package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

type deleteWorkspaceRepo struct {
	workspace *domain.Workspace
	getByID   string
}

func (r *deleteWorkspaceRepo) Create(context.Context, string, *domain.Workspace, []string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *deleteWorkspaceRepo) List(context.Context, string) ([]*domain.Workspace, error) {
	return []*domain.Workspace{r.workspace}, nil
}
func (r *deleteWorkspaceRepo) GetByName(context.Context, string, string) (*domain.Workspace, error) {
	return r.workspace, nil
}
func (r *deleteWorkspaceRepo) GetByID(_ context.Context, _, id string) (*domain.Workspace, error) {
	r.getByID = id
	return r.workspace, nil
}
func (r *deleteWorkspaceRepo) UpdateWorkspaceAll(context.Context, string, string, *string, *string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *deleteWorkspaceRepo) RollbackWorkspace(context.Context, string, string, domain.KnowledgeWorkspaceSnapshot, string, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (r *deleteWorkspaceRepo) Delete(context.Context, string, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
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

func (r *deleteDocRepo) Save(context.Context, string, string, *domain.Document) (bool, error) {
	return true, nil
}
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
func (r *deleteDocRepo) VisibleDocIDs(_ context.Context, _, _, _, _ string) ([]string, error) {
	ids := make([]string, 0, len(r.docs))
	for _, d := range r.docs {
		ids = append(ids, d.ID)
	}
	return ids, nil
}
func (r *deleteDocRepo) GetByID(context.Context, string, string, string) (*domain.Document, error) {
	return nil, domain.ErrDocumentNotFound
}
func (r *deleteDocRepo) SetDocAccess(context.Context, string, string, []string, []string) error {
	return nil
}
func (r *deleteDocRepo) CASReplace(context.Context, string, string, string, string, string, string, map[string]any, int) (bool, error) {
	return true, nil
}
func (r *deleteDocRepo) CASBeginDelete(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (r *deleteDocRepo) MarkBuiltinLegacy(context.Context, string, string, []string) error {
	return nil
}

type deleteVectorStore struct {
	deletedCollection string
	deletedDocIDs     []string
}

func (s *deleteVectorStore) CreateCollectionWithDim(context.Context, string, int) error { return nil }
func (s *deleteVectorStore) DeleteByDocumentIDs(_ context.Context, collection string, docIDs []string) error {
	s.deletedCollection = collection
	s.deletedDocIDs = append([]string(nil), docIDs...)
	return nil
}

func TestGetWorkspaceByIDUsesStableResourceID(t *testing.T) {
	repo := &deleteWorkspaceRepo{workspace: &domain.Workspace{ID: "ws-1", Name: "docs"}}
	service := NewWorkspaceService(repo, nil, zap.NewNop())
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	workspace, err := service.GetWorkspaceByID(context.Background(), "tenant-1", "ws-1")

	if err != nil {
		t.Fatalf("GetWorkspaceByID() error = %v", err)
	}
	if workspace.ID != "ws-1" || repo.getByID != "ws-1" {
		t.Fatalf("workspace ID = %q, repository lookup ID = %q", workspace.ID, repo.getByID)
	}
}

func TestDeleteDocumentRejectsProcessingDocument(t *testing.T) {
	docs := &deleteDocRepo{docs: []*domain.Document{{ID: "doc-1", IngestStatus: "processing"}}}
	vectors := &deleteVectorStore{}
	service := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{ID: "ws-1", Name: "docs"}}, nil, zap.NewNop())
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	service.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	service.SetDocRepo(docs)
	service.SetVectorStore(vectors)

	err := service.DeleteDocument(context.Background(), "tenant-1", "docs", "doc-1", "user-1")

	if !errors.Is(err, domain.ErrDocumentProcessing) {
		t.Fatalf("expected ErrDocumentProcessing, got %v", err)
	}
	if len(docs.deletedIDs) != 0 || len(vectors.deletedDocIDs) != 0 {
		t.Fatal("processing document must not trigger storage cleanup")
	}
}

func TestDeleteDocumentCleansVectorsBeforeDocumentRecord(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			docs := &deleteDocRepo{docs: []*domain.Document{{ID: "doc-1", IngestStatus: status}}}
			vectors := &deleteVectorStore{}
			service := NewWorkspaceService(&deleteWorkspaceRepo{workspace: &domain.Workspace{ID: "ws-1", Name: "docs"}}, nil, zap.NewNop())
			service.SetTenantRoleResolver(stubTenantRole{role: "owner"})
			service.SetTenantRoleResolver(stubTenantRole{role: "owner"})
			service.SetDocRepo(docs)
			service.SetVectorStore(vectors)

			if err := service.DeleteDocument(context.Background(), "tenant-1", "docs", "doc-1", "user-1"); err != nil {
				t.Fatalf("DeleteDocument() error = %v", err)
			}
			if len(vectors.deletedDocIDs) != 1 || vectors.deletedDocIDs[0] != "doc-1" {
				t.Fatalf("deleted vector document IDs = %v", vectors.deletedDocIDs)
			}
			if len(docs.deletedIDs) != 1 || docs.deletedIDs[0] != "doc-1" {
				t.Fatalf("deleted database document IDs = %v", docs.deletedIDs)
			}
		})
	}
}

// TestGetWorkspaceStatsReturnsEditors pins the stats detail response carrying
// the granted editor whitelist (I-1): the front-end derives canEdit /
// canRequestEditor from it, so the「申请编辑权限」按钮必须按 editors 回显。
func TestGetWorkspaceStatsReturnsEditors(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	ws := seedWorkspace(repo, "ws1")
	editors := newStubKnowledgeEditorRepo()
	editors.editors[ws.ID] = []string{"editor-a", "editor-b"}
	svc, ki := buildWorkspaceService(repo)
	svc.SetEditorRepo(editors)
	rec := &recordingDocRepo{ids: []string{"d1"}}
	rec.docs = []*domain.Document{{ID: "d1"}}
	svc.SetDocRepo(rec)
	ki.vectorStore = &fixedCountVectorStore{count: 0}

	res, err := svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "viewer-1")
	require.NoError(t, err)
	require.Equal(t, []string{"editor-a", "editor-b"}, res.Editors)
}

// TestGetWorkspaceStatsEditorsNilWithoutRepo pins nil-safe editors when the
// editor repo is not wired：JSON 层用 strSliceOrEmpty 渲染 []，前端 schema
// .default([]) 兜底。
func TestGetWorkspaceStatsEditorsNilWithoutRepo(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	seedWorkspace(repo, "ws1")
	svc, ki := buildWorkspaceService(repo)
	rec := &recordingDocRepo{ids: []string{"d1"}}
	rec.docs = []*domain.Document{{ID: "d1"}}
	svc.SetDocRepo(rec)
	ki.vectorStore = &fixedCountVectorStore{count: 0}

	res, err := svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "viewer-1")
	require.NoError(t, err)
	require.Nil(t, res.Editors)
}
