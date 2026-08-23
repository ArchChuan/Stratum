package port

import "context"

// ActorNameResolver 批量解析 actor 的用户名（display_name > github_login > actor_id 兜底）。
// 用于审批列表/详情的发起人、指派审批人、处理人昵称展示——审批 DTO 暴露原始 id
// 字段不可读，用户要求以昵称呈现。
//
// 实现位于 internal/iam/infrastructure/persistence（public.users 全局表，跨租户），
// 由 api/wiring 装配注入。查询失败返回错误（fail-closed：禁止默认名掩盖查询故障）；
// 查不到的 actor（system 占位符等）由实现回退 actor_id 原文。
type ActorNameResolver interface {
	ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error)
}
