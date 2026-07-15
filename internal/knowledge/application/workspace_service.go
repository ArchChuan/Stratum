package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Application-level sentinel errors. They alias domain errors so existing
// imports keep compiling while the HTTP error mapping table can match either.
var (
	ErrInvalidEmbeddingModel   = domain.ErrInvalidEmbeddingModel
	ErrInvalidQueryMode        = domain.ErrInvalidQueryMode
	ErrEmbeddingModelImmutable = domain.ErrEmbeddingModelImmutable
	ErrChunkSizeImmutable      = domain.ErrChunkSizeImmutable
	ErrChunkOverlapImmutable   = domain.ErrChunkOverlapImmutable
)

// collectionProvisioner is a minimal port for workspace vector collection lifecycle.
type collectionProvisioner interface {
	CreateCollectionWithDim(ctx context.Context, name string, dim int) error
	DeleteByDocumentIDs(ctx context.Context, collection string, docIDs []string) error
}

// vectorDim returns the vector dimension for the given embedding model.
func vectorDim(model string) int {
	switch model {
	case "text-embedding-v2", "text-embedding-v3", "text-embedding-v4":
		return 1024
	case "embedding-3":
		return 2048
	default:
		return 1536
	}
}

// CreateWorkspaceInput carries the application-level shape of POST /knowledge/workspaces.
type CreateWorkspaceInput struct {
	Name        string
	Description string
	Config      domain.WorkspaceConfig
}

// UpdateWorkspaceInput carries the application-level shape of PATCH /knowledge/workspaces/:name.
type UpdateWorkspaceInput struct {
	Name        *string
	Description *string
	Config      *domain.WorkspaceConfig
}

// WorkspaceStatsResult bundles the workspace metadata and milvus stats.
type WorkspaceStatsResult struct {
	Name        string
	Description string
	Config      domain.WorkspaceConfig
	Stats       map[string]any
}

// IngestUploadResult mirrors the JSON shape returned by POST /knowledge/ingest.
// Post-half-async: only DocumentID / Workspace / Status / TotalChunks are
// meaningful at accept time; front-end polls docs list for terminal state.
type IngestUploadResult struct {
	DocumentID  string
	Workspace   string
	Status      string
	TotalChunks int
	Errors      []string
}

// WorkspaceService orchestrates workspace CRUD + ingest validation.
type WorkspaceService struct {
	repo        port.WorkspaceRepo
	ingestSvc   *KnowledgeIngest
	docRepo     port.DocRepo
	vectorStore collectionProvisioner
	logger      *zap.Logger
}

// NewWorkspaceService constructs a WorkspaceService.
func NewWorkspaceService(repo port.WorkspaceRepo, ingestSvc *KnowledgeIngest, logger *zap.Logger) *WorkspaceService {
	return &WorkspaceService{repo: repo, ingestSvc: ingestSvc, logger: logger}
}

// SetDocRepo injects the optional document repo used for deduplication.
func (s *WorkspaceService) SetDocRepo(r port.DocRepo) { s.docRepo = r }

// SetVectorStore injects vector collection management for workspace lifecycle.
func (s *WorkspaceService) SetVectorStore(vs collectionProvisioner) { s.vectorStore = vs }

// CreateWorkspace builds the aggregate via the domain factory then persists it.
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, tenantID string, in CreateWorkspaceInput) (*domain.Workspace, error) {
	ws, err := domain.NewWorkspace(in.Name, in.Description, in.Config, domain.DefaultChunkSize, domain.DefaultTopK)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, tenantID, ws); err != nil {
		return nil, err
	}
	if s.vectorStore != nil {
		col := constants.CollectionName(tenantID, ws.ID)
		if err := s.vectorStore.CreateCollectionWithDim(ctx, col, vectorDim(ws.Config.EmbeddingModel)); err != nil {
			s.logger.Error("knowledge.workspace.create_collection_failed: rolling back db record",
				zap.String("tenant_id", tenantID),
				zap.String("workspace", in.Name),
				zap.String("collection", col),
				zap.Error(err))
			_ = s.repo.Delete(ctx, tenantID, ws.Name)
			return nil, fmt.Errorf("failed to create vector collection: %w", err)
		}
		s.logger.Info("knowledge.workspace.collection_created",
			zap.String("tenant_id", tenantID),
			zap.String("collection", col))
	}
	return ws, nil
}

// ListWorkspaces returns all workspaces for the tenant.
func (s *WorkspaceService) ListWorkspaces(ctx context.Context, tenantID string) ([]*domain.Workspace, error) {
	return s.repo.List(ctx, tenantID)
}

// UpdateWorkspace loads the aggregate, applies a partial update via the domain
// merge rule, then persists. Immutability/validation errors come from domain.
func (s *WorkspaceService) UpdateWorkspace(ctx context.Context, tenantID, name string, in UpdateWorkspaceInput) (*domain.Workspace, error) {
	current, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}

	if in.Name != nil && *in.Name != name {
		if err := s.repo.UpdateName(ctx, tenantID, name, *in.Name); err != nil {
			return nil, err
		}
		current.Name = *in.Name
		name = *in.Name
	}

	newCfg := current.Config
	if in.Config != nil {
		merged, mergeErr := current.Config.MergeUpdate(*in.Config)
		if mergeErr != nil {
			return nil, mergeErr
		}
		newCfg = merged
	}

	if err := s.repo.UpdateDescriptionAndConfig(ctx, tenantID, name, in.Description, newCfg); err != nil {
		return nil, err
	}
	current.UpdateConfig(newCfg)
	if in.Description != nil {
		current.UpdateDescription(*in.Description)
	}
	return current, nil
}

// GetWorkspaceStats fetches workspace metadata and milvus stats; stats errors degrade to {error: ...}.
func (s *WorkspaceService) GetWorkspaceStats(ctx context.Context, tenantID, name string) (*WorkspaceStatsResult, error) {
	ws, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}
	stats, statsErr := s.ingestSvc.GetWorkspaceStats(ctx, tenantID, ws.ID)
	if statsErr != nil {
		s.logger.Warn("failed to get milvus stats", zap.String("workspace", name), zap.Error(statsErr))
		stats = map[string]any{"error": statsErr.Error()}
	}
	if s.docRepo != nil {
		docCount, docErr := s.docRepo.CountByWorkspace(ctx, tenantID, ws.ID)
		if docErr != nil {
			s.logger.Warn("failed to get doc count", zap.String("workspace", name), zap.Error(docErr))
		} else {
			stats["doc_count"] = docCount
		}
	}
	return &WorkspaceStatsResult{
		Name:        name,
		Description: ws.Description,
		Config:      ws.Config,
		Stats:       stats,
	}, nil
}

// DeleteWorkspace cleans milvus + graph storage then removes the DB row.
func (s *WorkspaceService) DeleteWorkspace(ctx context.Context, tenantID, name string) error {
	ws, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return err
	}
	if err := s.ingestSvc.DeleteWorkspaceData(ctx, tenantID, ws.ID); err != nil {
		s.logger.Error("failed to clean workspace storage resources", zap.String("name", name), zap.Error(err))
		return fmt.Errorf("failed to clean storage: %w", err)
	}
	return s.repo.Delete(ctx, tenantID, name)
}

func (s *WorkspaceService) GetConfig(ctx context.Context, tenantID, workspace string) (domain.WorkspaceConfig, error) {
	return s.repo.GetConfigForUpload(ctx, tenantID, workspace)
}

func (s *WorkspaceService) GetWorkspace(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	return s.repo.GetByName(ctx, tenantID, name)
}

// IngestUpload reads the uploaded file and dispatches ingestion using the workspace's configured embedding model.
func (s *WorkspaceService) IngestUpload(ctx context.Context, tenantID, workspace string, fileHeader *multipart.FileHeader) (*IngestUploadResult, error) {
	ws, err := s.repo.GetByName(ctx, tenantID, workspace)
	if err != nil {
		return nil, err
	}

	file, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close() //nolint:errcheck

	fileData := make([]byte, fileHeader.Size)
	if _, err := file.Read(fileData); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(fileData))
	if s.docRepo != nil {
		if exists, err := s.docRepo.ExistsByHash(ctx, tenantID, ws.ID, hash); err != nil {
			s.logger.Warn("dedup check failed", zap.Error(err))
		} else if exists {
			return nil, domain.ErrDuplicateDocument
		}
	}

	documentID := uuid.Must(uuid.NewV7()).String()
	result, err := s.ingestSvc.IngestDocument(ctx, IngestDocumentRequest{
		TenantID:         tenantID,
		Workspace:        workspace,
		WorkspaceID:      ws.ID,
		EmbeddingModel:   ws.Config.EmbeddingModel,
		ChunkingStrategy: ws.Config.ChunkingStrategy,
		ChunkSize:        ws.Config.ChunkSize,
		ChunkOverlap:     ws.Config.ChunkOverlap,
		DocumentData:     fileData,
		FileName:         fileHeader.Filename,
		DocumentID:       documentID,
		ContentHash:      hash,
	})
	if err != nil {
		return nil, err
	}
	return &IngestUploadResult{
		DocumentID:  result.DocumentID,
		Workspace:   result.Workspace,
		Status:      result.Status,
		TotalChunks: result.TotalChunks,
		Errors:      result.Errors,
	}, nil
}

// DocumentView is the projection returned by ListDocuments — omits raw
// contents and exposes ingest lifecycle fields so the front-end can render
// status badges + poll for terminal state.
type DocumentView struct {
	ID               string
	Source           string
	ContentHash      string
	IngestStatus     string
	IngestError      string
	ProcessedChunks  int
	TotalChunks      int
	CreatedAt        time.Time
	IngestStartedAt  *time.Time
	IngestFinishedAt *time.Time
}

// ListDocuments returns the documents in a workspace with their ingest status.
// Used by GET /knowledge/workspaces/:name/documents and polled by the UI.
func (s *WorkspaceService) ListDocuments(ctx context.Context, tenantID, workspace string) ([]DocumentView, error) {
	if s.docRepo == nil {
		return []DocumentView{}, nil
	}
	ws, err := s.repo.GetByName(ctx, tenantID, workspace)
	if err != nil {
		return nil, err
	}
	docs, err := s.docRepo.List(ctx, tenantID, ws.ID)
	if err != nil {
		return nil, err
	}
	views := make([]DocumentView, len(docs))
	for i, d := range docs {
		views[i] = DocumentView{
			ID:               d.ID,
			Source:           d.Source,
			ContentHash:      d.ContentHash,
			IngestStatus:     d.IngestStatus,
			IngestError:      d.IngestError,
			ProcessedChunks:  d.ProcessedChunks,
			TotalChunks:      d.TotalChunks,
			CreatedAt:        d.CreatedAt,
			IngestStartedAt:  d.IngestStartedAt,
			IngestFinishedAt: d.IngestFinishedAt,
		}
	}
	return views, nil
}

// DeleteDocument removes a single document from a workspace.
// Returns ErrDocumentProcessing if the document is currently being ingested.
// Vectors are deleted before the database record to avoid orphaned embeddings.
func (s *WorkspaceService) DeleteDocument(ctx context.Context, tenantID, workspaceName, docID string) error {
	ws, err := s.repo.GetByName(ctx, tenantID, workspaceName)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	docs, err := s.docRepo.List(ctx, tenantID, ws.ID)
	if err != nil {
		return fmt.Errorf("list documents: %w", err)
	}

	var target *domain.Document
	for _, d := range docs {
		if d.ID == docID {
			target = d
			break
		}
	}
	if target == nil {
		return domain.ErrWorkspaceNotFound
	}

	if target.IngestStatus == "processing" {
		return domain.ErrDocumentProcessing
	}

	if s.vectorStore != nil {
		col := constants.CollectionName(tenantID, ws.ID)
		if err := s.vectorStore.DeleteByDocumentIDs(ctx, col, []string{docID}); err != nil {
			return fmt.Errorf("delete vectors: %w", err)
		}
	}

	return s.docRepo.Delete(ctx, tenantID, ws.ID, docID)
}
