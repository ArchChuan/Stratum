package port

import "context"

// TenantTierReader 读取租户 tier（§3.6 stratum）。消费方显式携带 tenantID，
// 与 ctx 解耦（评测消费链路 ctx 无 tenant）。实现由 wiring 适配 iam
// AdminTenantRepo：Tenant.Plan 直接透传为 tier 字符串。
type TenantTierReader interface {
	GetTenantTier(ctx context.Context, tenantID string) (string, error)
}
