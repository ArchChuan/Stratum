package persistence

import (
	"context"
	"fmt"
)

// PgActorNameResolver 批量解析 actor 昵称，满足 agent 包的 ActorNameResolver port。
// public.users 是全局表（非 tenant schema），无需 execTenant/search_path 切换。
//
// 展示名优先级 display_name > github_login > actor_id 原文；system 占位符
// （evaluation-worker 等无 users 行）或空值一律回退 actor_id 原文（fail-closed：
// 查询失败必须返回错误，禁止默认名掩盖故障）。
type PgActorNameResolver struct {
	pool pgxPool
}

// NewPgActorNameResolver 装配 actor 昵称解析器。
func NewPgActorNameResolver(pool pgxPool) *PgActorNameResolver {
	return &PgActorNameResolver{pool: pool}
}

// ResolveActorNames 返回 actorID -> 展示名 的映射。查不到的 id 不在映射中，
// 由调用方回退原文；空输入返回空映射（不触发查询）。
func (r *PgActorNameResolver) ResolveActorNames(ctx context.Context, actorIDs []string) (map[string]string, error) {
	names := make(map[string]string, len(actorIDs))
	if len(actorIDs) == 0 {
		return names, nil
	}
	// id::text = ANY($1) 而非 id = ANY($1)：public.users.id 是 uuid，而 actorID
	// 可能是 system 占位符或空串，uuid = ANY(text[]) 会被 PG 推断为 uuid[] 后逐
	// 元素 cast，非 uuid 值直接 invalid input syntax for type uuid (22P02) → 整个
	// 查询 500。text 比较对任意 actor 安全（与 audit loadActorNames 同模式）。
	rows, err := r.pool.Query(ctx, `SELECT id, COALESCE(display_name,''), COALESCE(github_login,'')
		FROM public.users WHERE id::text = ANY($1)`, actorIDs)
	if err != nil {
		return nil, fmt.Errorf("iam: resolve actor names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, displayName, githubLogin string
		if err := rows.Scan(&id, &displayName, &githubLogin); err != nil {
			return nil, fmt.Errorf("iam: scan actor name: %w", err)
		}
		names[id] = actorName(displayName, githubLogin, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iam: iterate actor names: %w", err)
	}
	return names, nil
}

// actorName 按 display_name > github_login > actor_id 原文兜底。
func actorName(displayName, githubLogin, fallback string) string {
	if displayName != "" {
		return displayName
	}
	if githubLogin != "" {
		return githubLogin
	}
	return fallback
}
