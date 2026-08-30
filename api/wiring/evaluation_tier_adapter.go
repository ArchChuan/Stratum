package wiring

import (
	"context"
	"fmt"

	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// tenantTierAdapter 把 iam AdminTenantRepo 适配为 evaluation 的 TenantTierReader：
// 租户 plan（free/pro）直接透传为 tier 字符串。
type tenantTierAdapter struct {
	repo iamport.AdminTenantRepo
}

func (a tenantTierAdapter) GetTenantTier(ctx context.Context, tenantID string) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	tenant, err := a.repo.Get(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("get tenant tier: %w", err)
	}
	return tenant.Plan, nil
}

// 编译期断言：tenantTierAdapter 满足 evaluation port。
var _ evalport.TenantTierReader = tenantTierAdapter{}
