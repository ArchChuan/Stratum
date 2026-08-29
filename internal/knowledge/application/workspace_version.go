package application

import (
	"context"
	"fmt"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
)

// WorkspaceVersionDTO 是工作区版本历史的响应形状，字段与 agent 的 VersionDTO
// 对齐（前端共用 VersionHistory 组件）。
type WorkspaceVersionDTO struct {
	ID            string
	VersionNo     int
	Status        string
	Source        string
	ContentHash   string
	CreatedBy     string
	CreatedByName string
	CreatedAt     string // RFC3339
	PublishedAt   string // RFC3339；未发布为空串
	IsCurrent     bool
	SafeSummary   map[string]any
}

// RollbackWorkspaceInput carries the actor performing the rollback and the
// target version (by ID) to restore.
type RollbackWorkspaceInput struct {
	ActorID   string
	VersionID string
}

func workspaceVersionToDTO(v versioningdomain.Version) WorkspaceVersionDTO {
	var publishedAt string
	if v.PublishedAt != nil {
		publishedAt = v.PublishedAt.UTC().Format(time.RFC3339)
	}
	return WorkspaceVersionDTO{
		ID:            v.ID,
		VersionNo:     v.RevisionNo,
		Status:        string(v.Status),
		Source:        string(v.Source),
		ContentHash:   v.ContentHash,
		CreatedBy:     v.CreatedBy,
		CreatedByName: v.CreatedByName,
		CreatedAt:     v.CreatedAt.UTC().Format(time.RFC3339),
		PublishedAt:   publishedAt,
		IsCurrent:     v.IsCurrent,
		SafeSummary:   v.SafeSummary,
	}
}

// ListWorkspaceVersions returns the workspace's product version history
// (newest first) with created_by display names resolved.
func (s *WorkspaceService) ListWorkspaceVersions(ctx context.Context, tenantID, name string) ([]WorkspaceVersionDTO, error) {
	if s.versionRepo == nil {
		return nil, fmt.Errorf("knowledge service list workspace versions: version repo not wired")
	}
	ws, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}
	versions, err := s.versionRepo.ListVersions(ctx, tenantID, versioningdomain.ResourceKindKnowledge, ws.ID)
	if err != nil {
		return nil, err
	}
	dtos := make([]WorkspaceVersionDTO, 0, len(versions))
	for _, v := range versions {
		dtos = append(dtos, workspaceVersionToDTO(v))
	}
	if err := s.resolveWorkspaceVersionNames(ctx, tenantID, dtos); err != nil {
		return nil, err
	}
	return dtos, nil
}

// resolveWorkspaceVersionNames 批量解析版本操作者昵称（display_name >
// github_login > actor_id）。nameResolver 为 nil 时跳过（id 原文展示）。
func (s *WorkspaceService) resolveWorkspaceVersionNames(ctx context.Context, tenantID string, versions []WorkspaceVersionDTO) error {
	if s.nameResolver == nil {
		return nil
	}
	actorIDs := make([]string, 0, len(versions))
	seen := make(map[string]struct{}, len(versions))
	for _, v := range versions {
		if _, ok := seen[v.CreatedBy]; ok {
			continue
		}
		seen[v.CreatedBy] = struct{}{}
		actorIDs = append(actorIDs, v.CreatedBy)
	}
	names, err := s.nameResolver.ResolveActorNames(ctx, actorIDs)
	if err != nil {
		return err
	}
	for i := range versions {
		if n, ok := names[versions[i].CreatedBy]; ok {
			versions[i].CreatedByName = n
		}
	}
	return nil
}

// RollbackWorkspace restores a deprecated historical version. The version
// payload is rebuilt into a snapshot, applied back to the workspace row, and
// active_version_id repointed at it — all in the repo's transaction. No new
// version is created. Returns the fresh workspace (re-read after the write).
func (s *WorkspaceService) RollbackWorkspace(ctx context.Context, tenantID, name string, in RollbackWorkspaceInput) (*domain.Workspace, error) {
	if s.versionRepo == nil {
		return nil, fmt.Errorf("knowledge service rollback workspace: version repo not wired")
	}
	current, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}
	target, found, err := s.versionRepo.GetVersion(ctx, tenantID, versioningdomain.ResourceKindKnowledge, current.ID, in.VersionID)
	if err != nil {
		return nil, err
	}
	// 仅 deprecated 历史版本可回滚（fail-closed，对齐 agent）。
	if !found || target.Status != versioningdomain.VersionStatusDeprecated {
		return nil, versioningdomain.ErrVersionNotFound
	}
	// 权限：沿用更新矩阵（owner/admin → editorActor="", 白名单 editor →
	// editorActor=actorID，repo 事务内重校验）。
	editorActor, err := s.resolveUpdateActor(ctx, tenantID, in.ActorID, current)
	if err != nil {
		return nil, err
	}
	snap, err := domain.SnapshotFromMap(target.Payload)
	if err != nil {
		return nil, fmt.Errorf("knowledge service rollback workspace: parse version payload: %w", err)
	}
	after := snap.ToWorkspace(current.ID)
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindKnowledge, current.ID, auditdomain.ChangeOpUpdate,
		in.ActorID, KnowledgeSafeProjection(current), KnowledgeSafeProjection(after))
	if err != nil {
		return nil, err
	}
	if err := s.repo.RollbackWorkspace(ctx, tenantID, name, snap, editorActor, in.VersionID, audit); err != nil {
		s.recordFailure(ctx, current.ID, "rollback", err)
		return nil, err
	}
	// 回滚后重读最新行（名称/描述/配置已被版本数据覆盖）。
	return s.repo.GetByID(ctx, tenantID, current.ID)
}
