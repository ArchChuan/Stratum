package wiring

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/infrastructure/persistence"
)

// Mechanism 承载机制基线（model_profiles）装配：Service 供管理面调用，
// BaselineResolver 供消费路径（memory 管线/压缩链路）取当前生效基线。
type Mechanism struct {
	Service *application.Service
	// BaselineResolver 按模型名解析机制基线（DB 档案 → embedded 种子兜底）。
	// 消费方在 per-tenant 解析出模型后调用；DB 缺失（dev 模式）时由
	// Service 内部回退种子，语义与改造前硬编码一致。
	BaselineResolver func(ctx context.Context, model string) (mechanismdomain.Baseline, error)
}

// buildMechanism 装配机制基线。DB 不可用时保持 nil（调用方 nil-check），
// 与其余组件缺库降级语义一致。
func (c *Container) buildMechanism(_ context.Context) error {
	db := c.dbOrNil()
	if db == nil {
		return nil
	}
	svc := application.NewService(persistence.NewProfileRepo(db))
	c.Mechanism = &Mechanism{
		Service: svc,
		BaselineResolver: func(ctx context.Context, model string) (mechanismdomain.Baseline, error) {
			return svc.GetEffective(ctx, model)
		},
	}
	return nil
}
