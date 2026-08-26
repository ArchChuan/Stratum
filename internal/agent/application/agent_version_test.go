package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
	versioningport "github.com/byteBuilderX/stratum/internal/versioning/domain/port"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockVersionRepo 是通用版本基座只读 port（resource_versions）的脚本化实现。
type mockVersionRepo struct {
	versions []versioningdomain.Version
	get      versioningdomain.Version
	getFound bool
	listErr  error
	getErr   error
}

func (m *mockVersionRepo) ListVersions(_ context.Context, _ string, _ versioningdomain.ResourceKind, _ string) ([]versioningdomain.Version, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.versions, nil
}

func (m *mockVersionRepo) GetVersion(_ context.Context, _ string, _ versioningdomain.ResourceKind, _, _ string) (versioningdomain.Version, bool, error) {
	if m.getErr != nil {
		return versioningdomain.Version{}, false, m.getErr
	}
	return m.get, m.getFound, nil
}

// fakeVersionNameResolver 固定返回预置昵称映射，可注入失败。
type fakeVersionNameResolver struct {
	names map[string]string
	err   error
}

func (f *fakeVersionNameResolver) ResolveActorNames(_ context.Context, ids []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if n, ok := f.names[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

// newVersionTestService 构造带版本基座与昵称解析的 AgentService（owner 角色）。
func newVersionTestService(repo *mockAgentRepo, vrepo versioningport.VersionRepo, resolver port.ActorNameResolver) *application.AgentService {
	reg := application.NewRegistry(repo, zap.NewNop())
	return application.NewAgentService(application.AgentServiceDeps{
		Registry:           reg,
		VersionRepo:        vrepo,
		ActorNameResolver:  resolver,
		TenantRoleResolver: stubTenantRole{role: "owner"},
		Logger:             zap.NewNop(),
	})
}

// TestAgentService_ListVersions 验证版本历史按时间倒序返回且昵称已解析。
func TestAgentService_ListVersions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := new(mockAgentRepo)
	vrepo := &mockVersionRepo{versions: []versioningdomain.Version{
		{ID: "v2", RevisionNo: 2, Status: versioningdomain.VersionStatusPublished, Source: versioningdomain.VersionSourceManual,
			ContentHash: "h2", CreatedBy: "user-1", CreatedAt: now, IsCurrent: true},
		{ID: "v1", RevisionNo: 1, Status: versioningdomain.VersionStatusDeprecated, Source: versioningdomain.VersionSourceManual,
			ContentHash: "h1", CreatedBy: "user-2", CreatedAt: now.Add(-time.Hour), PublishedAt: nil},
	}}
	svc := newVersionTestService(repo, vrepo, &fakeVersionNameResolver{names: map[string]string{
		"user-1": "Alice", "user-2": "Bob",
	}})

	dtos, err := svc.ListVersions(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Len(t, dtos, 2)
	require.Equal(t, "v2", dtos[0].ID)
	require.Equal(t, "Alice", dtos[0].CreatedByName)
	require.True(t, dtos[0].IsCurrent)
	require.Equal(t, "v1", dtos[1].ID)
	require.Equal(t, "Bob", dtos[1].CreatedByName)
	require.Equal(t, "", dtos[1].PublishedAt, "未发布版本 PublishedAt 为空串")
	require.Equal(t, "h2", dtos[0].ContentHash)
	require.Equal(t, string(versioningdomain.VersionStatusDeprecated), dtos[1].Status)
}

// TestAgentService_ListVersions_UnwiredFailsClosed 验证未装配版本基座时 fail-closed。
func TestAgentService_ListVersions_UnwiredFailsClosed(t *testing.T) {
	repo := new(mockAgentRepo)
	svc := newVersionTestService(repo, nil, nil)
	_, err := svc.ListVersions(context.Background(), "agent-1")
	require.ErrorContains(t, err, "version repo not wired")
}

// TestAgentService_ListVersions_ResolverFailurePropagates 验证昵称解析失败向上传播。
func TestAgentService_ListVersions_ResolverFailurePropagates(t *testing.T) {
	repo := new(mockAgentRepo)
	vrepo := &mockVersionRepo{versions: []versioningdomain.Version{
		{ID: "v1", CreatedBy: "user-1"},
	}}
	svc := newVersionTestService(repo, vrepo, &fakeVersionNameResolver{err: errors.New("iam down")})
	_, err := svc.ListVersions(context.Background(), "agent-1")
	require.ErrorContains(t, err, "resolve version names")
}

// TestAgentService_ListVersions_ResolverNilKeepsRawID 验证未注入解析器时保留原文。
func TestAgentService_ListVersions_ResolverNilKeepsRawID(t *testing.T) {
	repo := new(mockAgentRepo)
	vrepo := &mockVersionRepo{versions: []versioningdomain.Version{
		{ID: "v1", CreatedBy: "user-1"},
	}}
	svc := newVersionTestService(repo, vrepo, nil)
	dtos, err := svc.ListVersions(context.Background(), "agent-1")
	require.NoError(t, err)
	require.Equal(t, "", dtos[0].CreatedByName, "nil resolver 不填充昵称，前端回退 raw id")
}

func TestAgentService_Rollback(t *testing.T) {
	ctx := context.Background()
	targetCfg := &domain.AgentConfig{
		ID: "agent-1", Name: "historical", Description: "desc", Type: domain.ReActAgent,
		SystemPrompt: "old prompt", LLMModel: "qwen-plus", MaxIterations: 5, CreatedBy: "user-1",
	}
	target := versioningdomain.Version{
		ID: "v1", RevisionNo: 1, Status: versioningdomain.VersionStatusDeprecated,
		Source: versioningdomain.VersionSourceRollback, Payload: domain.SnapshotFromConfig(targetCfg).Map(),
	}

	repo := new(mockAgentRepo)
	existing := &domain.AgentConfig{ID: "agent-1", Name: "current", Type: domain.ReActAgent,
		SystemPrompt: "current prompt", LLMModel: "qwen-plus", MaxIterations: 3, CreatedBy: "user-1"}
	repo.On("Get", mock.Anything, "agent-1").Return(existing, true, nil).Once()
	repo.On("Rollback", mock.Anything, mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		cfg := args.Get(1).(*domain.AgentConfig)
		require.Equal(t, "historical", cfg.Name, "回滚写入的 cfg 由目标版本快照重建")
		require.Equal(t, "old prompt", cfg.SystemPrompt)
		require.Equal(t, "user-1", cfg.CreatedBy, "created_by 保留原始创建者")
	})
	repo.On("Get", mock.Anything, "agent-1").Return(targetCfg, true, nil).Once()
	svc := newVersionTestService(repo, &mockVersionRepo{get: target, getFound: true}, nil)

	dto, err := svc.Rollback(ctx, "agent-1", application.RollbackAgentInput{ActorID: "user-1", VersionID: "v1"})
	require.NoError(t, err)
	require.Equal(t, "historical", dto.Name)
	repo.AssertExpectations(t)
}

func TestAgentService_Rollback_MissingAgentFailsNotFound(t *testing.T) {
	repo := new(mockAgentRepo)
	repo.On("Get", mock.Anything, "agent-1").Return(nil, false, nil).Once()
	svc := newVersionTestService(repo, &mockVersionRepo{}, nil)
	_, err := svc.Rollback(context.Background(), "agent-1", application.RollbackAgentInput{ActorID: "user-1", VersionID: "v1"})
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestAgentService_Rollback_NonDeprecatedOrMissingTargetFailsVersionNotFound(t *testing.T) {
	ctx := context.Background()
	existing := &domain.AgentConfig{ID: "agent-1", Name: "current", Type: domain.ReActAgent, CreatedBy: "user-1"}

	// 目标是 published（非可回滚历史版本）。
	repo := new(mockAgentRepo)
	repo.On("Get", mock.Anything, "agent-1").Return(existing, true, nil).Once()
	published := versioningdomain.Version{ID: "v2", Status: versioningdomain.VersionStatusPublished}
	svc := newVersionTestService(repo, &mockVersionRepo{get: published, getFound: true}, nil)
	_, err := svc.Rollback(ctx, "agent-1", application.RollbackAgentInput{ActorID: "user-1", VersionID: "v2"})
	require.ErrorIs(t, err, versioningdomain.ErrVersionNotFound)

	// 目标不存在。
	repo2 := new(mockAgentRepo)
	repo2.On("Get", mock.Anything, "agent-1").Return(existing, true, nil).Once()
	svc2 := newVersionTestService(repo2, &mockVersionRepo{getFound: false}, nil)
	_, err = svc2.Rollback(ctx, "agent-1", application.RollbackAgentInput{ActorID: "user-1", VersionID: "nope"})
	require.ErrorIs(t, err, versioningdomain.ErrVersionNotFound)
}
