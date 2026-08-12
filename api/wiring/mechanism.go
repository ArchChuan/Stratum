package wiring

import (
	"context"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/mechanism/application"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/internal/mechanism/infrastructure/persistence"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"

	"github.com/byteBuilderX/stratum/pkg/constants"
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

// mechanismBaselineForTenant 构造 memory 消费方的基线解析闭包（wiring 唯一
// 适配点：mechanism domain.Baseline → memport.MechanismBaseline）。
// cfgModel 是消费路径现状默认模型（如 pipeline EnrichModel env 值）：worker
// 场景只有 tenantID 无模型可查，按族前缀共享基线即可，租户差异化由 gateway
// 承载。解析失败（DB 故障）：Warn + 返回空基线，消费方回退自身硬编码
// （等价改造前现状）；基线是配置源而非授权，回退不构成安全降级。
func (c *Container) mechanismBaselineForTenant(cfgModel string) memport.MechanismBaselineResolver {
	return func(ctx context.Context, tenantID string) (memport.MechanismBaseline, error) {
		if c.Mechanism == nil || c.Mechanism.BaselineResolver == nil {
			return memport.MechanismBaseline{}, nil
		}
		// agent 压缩链构造点无 ctx 可传（factory 签名），统一短超时兜底 DB 悬挂。
		bctx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		defer cancel()
		b, err := c.Mechanism.BaselineResolver(bctx, cfgModel)
		if err != nil {
			c.Logger.Warn("mechanism baseline resolve failed, keep consumer fallback",
				zap.String("model", cfgModel), zap.Error(err))
			return memport.MechanismBaseline{}, nil
		}
		return memport.MechanismBaseline{
			MemoryExtraction: b.Prompts.MemoryExtraction,
			MemorySummary:    b.Prompts.MemorySummary,
			MemoryEnrichment: b.Prompts.MemoryEnrichment,
			MemorySummarize:  b.Prompts.MemorySummarize,
			MemorySupersede:  b.Prompts.MemorySupersede,
			EnrichModel:      b.Models.EnrichModel,
			SummaryModel:     b.Models.SummaryModel,
		}, nil
	}
}
