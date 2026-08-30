package port

import "context"

// ActorNameResolver 批量解析 actor 的用户名（display_name > github_login >
// actor_id 兜底）。实现位于 internal/iam/infrastructure/persistence
// （public.users 全局表，跨租户），由 api/wiring 装配注入。查询失败返回错误
// （fail-closed：禁止默认名掩盖查询故障）；查不到的 actor 由实现回退 actor_id
// 原文。镜像 internal/agent/domain/port/actor_name_resolver.go。
type ActorNameResolver interface {
	ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error)
}
