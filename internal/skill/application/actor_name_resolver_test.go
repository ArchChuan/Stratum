package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubActorNameResolver 实现 port.ActorNameResolver，供昵称解析用例注入。
type stubActorNameResolver struct {
	names map[string]string
	err   error
}

func (s *stubActorNameResolver) ResolveActorNames(_ context.Context, actorIDs []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(actorIDs))
	for _, id := range actorIDs {
		out[id] = s.names[id]
	}
	return out, nil
}

// TestVersionServiceListRevisionsResolvesNames 验证版本历史「操作者」昵称填充：
// 命中 display 名使用昵称；未命中的 actor 回退原文（不在映射中，CreatedByName 为空）。
func TestVersionServiceListRevisionsResolvesNames(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-1", domain.VersionStatusPublished, "user-1")
	seedRevision(t, repo, "s1", "rev-2", domain.VersionStatusDeprecated, "system-worker")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetActorNameResolver(&stubActorNameResolver{names: map[string]string{"user-1": "张三"}})

	revisions, err := svc.ListRevisions(context.Background(), "s1")
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	names := make(map[string]string)
	for _, rev := range revisions {
		names[rev.CreatedBy] = rev.CreatedByName
	}
	assert.Equal(t, "张三", names["user-1"], "命中昵称时展示昵称")
	assert.Equal(t, "", names["system-worker"], "未命中时回退原文(空 CreatedByName)")
}

// TestVersionServiceListRevisionsNameResolverFailure 验证 fail-closed：昵称查询
// 失败必须传播错误，禁止默认名掩盖查询故障。
func TestVersionServiceListRevisionsNameResolverFailure(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())
	svc.SetActorNameResolver(&stubActorNameResolver{err: errors.New("db down")})

	_, err := svc.ListRevisions(context.Background(), "s1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "resolve actor names")
}

// TestVersionServiceListRevisionsNilResolverDegrades 验证未注入解析器时保留 raw id
// 展示（降级，不报错）—— 与其它服务注入模式一致。
func TestVersionServiceListRevisionsNilResolverDegrades(t *testing.T) {
	repo := newFakeVersionRepo()
	seedRevision(t, repo, "s1", "rev-1", domain.VersionStatusPublished, "user-1")
	svc := NewVersionService(repo, zap.NewNop())

	revisions, err := svc.ListRevisions(context.Background(), "s1")
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	assert.Empty(t, revisions[0].CreatedByName)
	assert.Equal(t, "user-1", revisions[0].CreatedBy)
}

// TestSkillRevisionContentHashIgnoresCreatedByName 验证 CreatedByName 是 display-only：
// 不影响 content hash，保证旧版本 hash 在加字段后保持不变（乐观并发基线稳定）。
func TestSkillRevisionContentHashIgnoresCreatedByName(t *testing.T) {
	base := domain.SkillRevision{Name: "n", Description: "d", Instructions: "i", CreatedBy: "user-1"}
	withName := base
	withName.CreatedByName = "张三"

	hashBase, err := base.ComputeContentHash()
	require.NoError(t, err)
	hashWithName, err := withName.ComputeContentHash()
	require.NoError(t, err)
	assert.Equal(t, hashBase, hashWithName)
}

var _ port.ActorNameResolver = (*stubActorNameResolver)(nil)
