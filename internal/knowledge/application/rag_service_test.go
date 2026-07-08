package application

import (
	"context"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"go.uber.org/zap"
)

func TestNewKnowledgeIngest(t *testing.T) {
	logger := zap.NewNop()
	ingest := NewKnowledgeIngest(nil, nil, nil, nil, logger)

	if ingest == nil {
		t.Error("expected KnowledgeIngest to be non-nil")
	}
}

func TestNewRAGService(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)

	if service == nil {
		t.Error("expected RAGService to be non-nil")
	}
}

func TestRAGQueryKeywordUsesWorkspaceID(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{
		chunks: []domain.Chunk{{ID: "chunk-1", DocID: "doc-1", Text: "content", Index: 0}},
	}
	service.SetChunkRepo(chunks)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:    "tenant-1",
		Workspace:   "项目资料",
		WorkspaceID: "019047ac-0000-7000-9000-000000000001",
		Question:    "如何申请",
		Mode:        "keyword",
		TopK:        3,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.workspaceID != "019047ac-0000-7000-9000-000000000001" {
		t.Fatalf("expected keyword search to use workspace ID, got %q", chunks.workspaceID)
	}
}

func TestRAGQueryKeywordResolvesWorkspaceIDByName(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{}
	service.SetChunkRepo(chunks)
	service.SetWorkspaceRepo(&recordingWorkspaceRepo{
		workspace: &domain.Workspace{
			ID:   "019047ac-0000-7000-9000-000000000002",
			Name: "产品文档",
		},
	})

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:  "tenant-1",
		Workspace: "产品文档",
		Question:  "如何申请",
		Mode:      "keyword",
		TopK:      3,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.workspaceID != "019047ac-0000-7000-9000-000000000002" {
		t.Fatalf("expected keyword search to use resolved workspace ID, got %q", chunks.workspaceID)
	}
}

func TestRAGQueryKeywordDefaultsTopK(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{}
	service.SetChunkRepo(chunks)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:    "tenant-1",
		WorkspaceID: "workspace-1",
		Question:    "如何申请",
		Mode:        "keyword",
		TopK:        0,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.topK != 5 {
		t.Fatalf("expected non-positive TopK to default to 5, got %d", chunks.topK)
	}
}

func TestRAGQueryKeywordRequiresWorkspaceIDWhenNameCannotBeResolved(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	service.SetChunkRepo(&recordingChunkRepo{})

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1",
		Question: "如何申请",
		Mode:     "keyword",
		TopK:     3,
	})
	if err == nil {
		t.Fatal("expected keyword query without workspace ID to fail, got nil")
	}
	if !strings.Contains(err.Error(), "requires workspace ID") {
		t.Fatalf("expected workspace ID error, got %v", err)
	}
}

type recordingChunkRepo struct {
	workspaceID string
	topK        int
	chunks      []domain.Chunk
}

func (r *recordingChunkRepo) InsertBatch(ctx context.Context, tenantID, workspaceID string, chunks []domain.Chunk) error {
	r.workspaceID = workspaceID
	return nil
}

func (r *recordingChunkRepo) KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error) {
	r.workspaceID = workspaceID
	r.topK = topK
	return r.chunks, nil
}

func (r *recordingChunkRepo) DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error {
	r.workspaceID = workspaceID
	return nil
}

func (r *recordingChunkRepo) InsertParentBatch(_ context.Context, _, _ string, _ []port.ParentChunk) error {
	return nil
}

func (r *recordingChunkRepo) GetParentByID(_ context.Context, _, _, _ string) (*port.ParentChunk, error) {
	return nil, nil
}

func (r *recordingChunkRepo) GetChunksByIDs(_ context.Context, _, _ string, _ []string) ([]domain.Chunk, error) {
	return nil, nil
}

type recordingWorkspaceRepo struct {
	workspace *domain.Workspace
}

func (r *recordingWorkspaceRepo) Create(ctx context.Context, tenantID string, ws *domain.Workspace) error {
	return nil
}

func (r *recordingWorkspaceRepo) GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	return r.workspace, nil
}

func (r *recordingWorkspaceRepo) List(ctx context.Context, tenantID string) ([]*domain.Workspace, error) {
	return nil, nil
}

func (r *recordingWorkspaceRepo) UpdateDescriptionAndConfig(ctx context.Context, tenantID, name string, description *string, cfg domain.WorkspaceConfig) error {
	return nil
}

func (r *recordingWorkspaceRepo) UpdateName(ctx context.Context, tenantID, oldName, newName string) error {
	return nil
}

func (r *recordingWorkspaceRepo) Delete(ctx context.Context, tenantID, name string) error {
	return nil
}

func (r *recordingWorkspaceRepo) GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error) {
	return domain.WorkspaceConfig{}, nil
}

func (r *recordingWorkspaceRepo) GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error) {
	if r.workspace != nil && r.workspace.ID == id {
		return r.workspace.Config, nil
	}
	return domain.WorkspaceConfig{}, nil
}
