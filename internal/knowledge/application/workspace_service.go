package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformknowledge"
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
	DeleteByDocumentIDs(ctx context.Context, collectionName string, docIDs []string) error
}

// CreateWorkspaceInput carries the application-level shape of POST /knowledge/workspaces.
type CreateWorkspaceInput struct {
	Name        string
	Description string
	Config      domain.WorkspaceConfig
	// Editors are validated (role admin/owner) and persisted in the same
	// transaction as the workspace row.
	Editors []string
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
	roles       port.TenantRoleResolver
	editorRepo  port.ResourceEditorRepo
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

// SetTenantRoleResolver injects the tenant role resolver used by ownership
// checks. A nil resolver fails all writes closed (ownership unverifiable).
func (s *WorkspaceService) SetTenantRoleResolver(r port.TenantRoleResolver) { s.roles = r }

// SetEditorRepo injects the resource editor repository used by the update
// editor path and the SetEditors management endpoint. A nil repo denies
// editor grants entirely (fail closed).
func (s *WorkspaceService) SetEditorRepo(r port.ResourceEditorRepo) { s.editorRepo = r }

// isPlatformManaged reports whether a workspace is owned by the platform and
// must not be mutated through user-facing APIs.
func isPlatformManaged(ws *domain.Workspace) bool {
	return ws != nil && (ws.SystemKey == platformknowledge.SystemWorkspaceKey ||
		ws.ManagementMode == platformknowledge.ManagementPlatform)
}

// CreateWorkspace builds the aggregate via the domain factory then persists it.
func (s *WorkspaceService) CreateWorkspace(ctx context.Context, tenantID string, in CreateWorkspaceInput, actorID string) (*domain.Workspace, error) {
	// create encodes "the creator owns the resource": only owner/admin may create.
	if err := s.checkOwnership(ctx, tenantID, actorID, actorID, nil); err != nil {
		return nil, err
	}
	ws, err := domain.NewWorkspace(in.Name, in.Description, in.Config, domain.DefaultChunkSize, domain.DefaultTopK)
	if err != nil {
		return nil, err
	}
	ws.CreatedBy = actorID
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, ws.ID, auditdomain.ChangeOpCreate,
		actorID, nil, KnowledgeSafeProjection(ws))
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, tenantID, ws, in.Editors, audit); err != nil {
		return nil, err
	}
	if s.vectorStore != nil {
		col := constants.CollectionName(tenantID, ws.ID)
		if err := s.vectorStore.CreateCollectionWithDim(ctx, col, constants.DimensionForModel(ws.Config.EmbeddingModel)); err != nil {
			s.logger.Error("knowledge.workspace.create_collection_failed: rolling back db record",
				zap.String("tenant_id", tenantID),
				zap.String("workspace", in.Name),
				zap.String("collection", col),
				zap.Error(err))
			_ = s.repo.Delete(ctx, tenantID, ws.Name, nil)
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
func (s *WorkspaceService) UpdateWorkspace(ctx context.Context, tenantID, name string, in UpdateWorkspaceInput, actorID string) (*domain.Workspace, error) {
	current, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}
	if isPlatformManaged(current) {
		return nil, domain.ErrPlatformManagedWorkspace
	}
	// Base matrix: owner passes, creator-admin passes, everyone else needs the
	// editor grant. editorActor is carried into the write transaction so the
	// repo re-validates role + editor membership (TOCTOU closure).
	editorActor, err := s.resolveUpdateActor(ctx, tenantID, actorID, current)
	if err != nil {
		return nil, err
	}
	before := KnowledgeSafeProjection(current)

	var renameTo *string
	if in.Name != nil && *in.Name != name {
		renameTo = in.Name
	}

	newCfg, after, err := applyWorkspaceUpdate(current, in, renameTo)
	if err != nil {
		return nil, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, current.ID, auditdomain.ChangeOpUpdate,
		actorID, before, KnowledgeSafeProjection(after))
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateWorkspaceAll(ctx, tenantID, name, renameTo, in.Description, newCfg, editorActor, audit); err != nil {
		return nil, err
	}
	// after is the merged copy; no further in-memory sync needed.
	return after, nil
}

// resolveUpdateActor applies the ownership matrix with the editor grant on
// the update path. Owner and creator-admin pass with an empty editorActor
// (the write proceeds without editor revalidation); an actor granted editor
// rights passes with editorActor set, which the repo re-validates inside the
// write transaction. Fail closed on missing repo, list failure or denial.
func (s *WorkspaceService) resolveUpdateActor(ctx context.Context, tenantID, actorID string, current *domain.Workspace) (string, error) {
	if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, nil); err == nil {
		return "", nil
	}
	if s.editorRepo == nil {
		return "", domain.ErrForbidden
	}
	editors, err := s.editorRepo.ListEditors(ctx, tenantID, current.ID)
	if err != nil {
		return "", err
	}
	if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, editors); err != nil {
		return "", err
	}
	return actorID, nil
}

// ListEditors returns the granted editor set of a workspace (detail prefill).
func (s *WorkspaceService) ListEditors(ctx context.Context, tenantID, workspaceID string) ([]string, error) {
	if s.editorRepo == nil {
		return nil, nil
	}
	return s.editorRepo.ListEditors(ctx, tenantID, workspaceID)
}

// SetEditors replaces the editor set of a workspace. Only creator/owner may
// grant editors (an editor can never grant delete rights on a resource they
// merely edit). The swap happens in one transaction with the audit row; each
// editor id must hold role admin or owner at write time.
func (s *WorkspaceService) SetEditors(ctx context.Context, tenantID, name string, editorIDs []string, actorID string) error {
	if s.editorRepo == nil {
		return fmt.Errorf("workspace service set editors: editor repo not wired")
	}
	current, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return err
	}
	if isPlatformManaged(current) {
		return domain.ErrPlatformManagedWorkspace
	}
	// Editors can never grant delete rights, so SetEditors reuses the
	// creator/owner-only base matrix.
	if err := s.checkOwnership(ctx, tenantID, actorID, current.CreatedBy, nil); err != nil {
		return err
	}
	before, err := s.editorRepo.ListEditors(ctx, tenantID, current.ID)
	if err != nil {
		return fmt.Errorf("workspace service set editors: list editors: %w", err)
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, current.ID, auditdomain.ChangeOpUpdate, actorID,
		knowledgeSafeProjectionWithEditors(current, before), knowledgeSafeProjectionWithEditors(current, editorIDs))
	if err != nil {
		return err
	}
	if err := s.editorRepo.ReplaceEditors(ctx, tenantID, current.ID, editorIDs, actorID, audit); err != nil {
		return err
	}
	s.logger.Info("knowledge editors updated", zap.String("workspace", name), zap.Int("count", len(editorIDs)))
	return nil
}

// applyWorkspaceUpdate merges the partial input into a copy of current. It
// returns the merged config (for persistence) and the projected post-state
// (for the audit after-projection) in one pass.
func applyWorkspaceUpdate(current *domain.Workspace, in UpdateWorkspaceInput, renameTo *string) (domain.WorkspaceConfig, *domain.Workspace, error) {
	newCfg := current.Config
	if in.Config != nil {
		merged, err := current.Config.MergeUpdate(*in.Config)
		if err != nil {
			return domain.WorkspaceConfig{}, nil, err
		}
		newCfg = merged
	}
	after := *current
	if renameTo != nil {
		after.Name = *renameTo
	}
	after.UpdateConfig(newCfg)
	if in.Description != nil {
		after.UpdateDescription(*in.Description)
	}
	return newCfg, &after, nil
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
func (s *WorkspaceService) DeleteWorkspace(ctx context.Context, tenantID, name, actorID string) error {
	ws, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return err
	}
	if isPlatformManaged(ws) {
		return domain.ErrPlatformManagedWorkspace
	}
	if err := s.checkOwnership(ctx, tenantID, actorID, ws.CreatedBy, nil); err != nil {
		return err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, ws.ID, auditdomain.ChangeOpDelete,
		actorID, KnowledgeSafeProjection(ws), nil)
	if err != nil {
		return err
	}
	if err := s.ingestSvc.DeleteWorkspaceData(ctx, tenantID, ws.ID); err != nil {
		s.logger.Error("failed to clean workspace storage resources", zap.String("name", name), zap.Error(err))
		return fmt.Errorf("failed to clean storage: %w", err)
	}
	return s.repo.Delete(ctx, tenantID, name, audit)
}

func (s *WorkspaceService) GetConfig(ctx context.Context, tenantID, workspace string) (domain.WorkspaceConfig, error) {
	return s.repo.GetConfigForUpload(ctx, tenantID, workspace)
}

func (s *WorkspaceService) GetWorkspace(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	return s.repo.GetByName(ctx, tenantID, name)
}

func (s *WorkspaceService) GetWorkspaceByID(ctx context.Context, tenantID, id string) (*domain.Workspace, error) {
	return s.repo.GetByID(ctx, tenantID, id)
}

// ListSnapshotDocuments returns document metadata used to fingerprint an
// immutable evaluation snapshot. Document bodies remain in retrieval stores.
func (s *WorkspaceService) ListSnapshotDocuments(
	ctx context.Context, tenantID, workspaceID string,
) ([]*domain.Document, error) {
	if s == nil || s.docRepo == nil {
		return nil, errors.New("knowledge document repository unavailable")
	}
	return s.docRepo.List(ctx, tenantID, workspaceID)
}

// IngestUpload reads the uploaded file and dispatches ingestion using the workspace's configured embedding model.
func (s *WorkspaceService) IngestUpload(ctx context.Context, tenantID, workspace string, fileHeader *multipart.FileHeader) (*IngestUploadResult, error) {
	ws, err := s.repo.GetByName(ctx, tenantID, workspace)
	if err != nil {
		return nil, err
	}
	if isPlatformManaged(ws) {
		return nil, domain.ErrPlatformManagedWorkspace
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

// findDocument returns the document with the given ID within a workspace, or
// ErrDocumentNotFound.
func (s *WorkspaceService) findDocument(ctx context.Context, tenantID, workspaceID, documentID string) (*domain.Document, error) {
	docs, err := s.docRepo.List(ctx, tenantID, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		if doc.ID == documentID {
			return doc, nil
		}
	}
	return nil, domain.ErrDocumentNotFound
}

// DeleteDocument removes a terminal document from vector and relational storage.
// Processing documents are rejected because their background ingest job may still write data.
func (s *WorkspaceService) DeleteDocument(ctx context.Context, tenantID, workspace, documentID string) error {
	if s.docRepo == nil || s.vectorStore == nil {
		return errors.New("knowledge document storage is not configured")
	}
	ws, err := s.repo.GetByName(ctx, tenantID, workspace)
	if err != nil {
		return err
	}
	if isPlatformManaged(ws) {
		return domain.ErrPlatformManagedWorkspace
	}
	target, err := s.findDocument(ctx, tenantID, ws.ID, documentID)
	if err != nil {
		return err
	}
	if target.IngestStatus == constants.IngestStatusProcessing {
		return domain.ErrDocumentProcessing
	}
	collection := constants.CollectionName(tenantID, ws.ID)
	if err := s.vectorStore.DeleteByDocumentIDs(ctx, collection, []string{documentID}); err != nil {
		return fmt.Errorf("delete document vectors: %w", err)
	}
	if err := s.docRepo.Delete(ctx, tenantID, ws.ID, documentID); err != nil {
		return fmt.Errorf("delete document records: %w", err)
	}
	return nil
}
