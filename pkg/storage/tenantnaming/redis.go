package tenantnaming

import (
	"context"
	"fmt"
	"strings"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// RedisKeyPrefix 是所有 Agent 执行流 key 的公共前缀。
const RedisKeyPrefix = "agent"

// TenantKey 返回租户命名空间化的 Redis key，形如
// "{prefix}:{tenantID}:{part1}:{part2}..."。
//
// fail closed：租户上下文缺失或为空即报错，绝不退化成无命名空间的 key——
// execution_id 是客户端可控的（见 executionIDOrNew），只由它构成的 key 会让
// 提交别人 execution_id 的调用方读走别人的 token 流（spec D4）。
//
// part 含 ':' 同样报错：否则调用方可用 execution_id="x:2" 伪造出 generation
// 段，拼出另一个执行的 key。
func TenantKey(ctx context.Context, prefix string, parts ...string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	if prefix == "" {
		return "", fmt.Errorf("tenantnaming: prefix is empty")
	}
	if strings.ContainsRune(prefix, ':') {
		return "", fmt.Errorf("tenantnaming: prefix %q contains ':'", prefix)
	}
	segments := make([]string, 0, len(parts)+2)
	segments = append(segments, prefix, tc.TenantID)
	for _, p := range parts {
		if p == "" {
			return "", fmt.Errorf("tenantnaming: empty key part")
		}
		if strings.ContainsRune(p, ':') {
			return "", fmt.Errorf("tenantnaming: key part %q contains ':'", p)
		}
		segments = append(segments, p)
	}
	return strings.Join(segments, ":"), nil
}
