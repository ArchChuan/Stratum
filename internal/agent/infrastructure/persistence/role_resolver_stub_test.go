package persistence_test

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// integrationRoleStub 让持久化集成测试在角色现查路径上提供固定角色。
// 生产 wiring 由 injectTenantRoleResolvers 注入 DB-backed adapter；测试用 stub
// 避免触发 service 的 fail-closed 路径（resolver 缺失 = "role resolver unavailable"）。
// 角色 fail-closed 语义由 application 层单测覆盖，此处不重复断言。
type integrationRoleStub struct{ role string }

var _ port.TenantRoleResolver = integrationRoleStub{}

func (s integrationRoleStub) ResolveTenantRole(context.Context, string, string) (string, error) {
	return s.role, nil
}
