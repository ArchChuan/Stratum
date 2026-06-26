package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"mime/multipart"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	skillpkg "github.com/byteBuilderX/stratum/internal/skill/domain"
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
type IngestUploadResult struct {
	DocumentID   string
	Workspace    string
	TotalChunks  int
	TotalVectors int
	Duration     string
	Errors       []string
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
	ws, err := domain.NewWorkspace(in.Name, in.Description, in.Config, skillpkg.DefaultChunkSize, skillpkg.DefaultTopK)
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
		TenantID:       tenantID,
		Workspace:      workspace,
		WorkspaceID:    ws.ID,
		EmbeddingModel: ws.Config.EmbeddingModel,
		DocumentData:   fileData,
		FileName:       fileHeader.Filename,
		DocumentID:     documentID,
	})
	if err != nil {
		return nil, err
	}
	if s.docRepo != nil {
		if saveErr := s.docRepo.Save(ctx, tenantID, ws.ID, &domain.Document{
			ID: documentID, KBID: ws.ID, Source: fileHeader.Filename, ContentHash: hash,
		}); saveErr != nil {
			s.logger.Warn("failed to save doc metadata", zap.Error(saveErr))
		}
	}
	return &IngestUploadResult{
		DocumentID:   result.DocumentID,
		Workspace:    result.Workspace,
		TotalChunks:  result.TotalChunks,
		TotalVectors: result.TotalVectors,
		Duration:     result.Duration.String(),
		Errors:       result.Errors,
	}, nil
}

// (no compile-time aliasing required — errors are imported via `domain`.)
