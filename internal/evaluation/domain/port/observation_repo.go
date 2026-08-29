package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// ObservationRepository 持久化运行态观测明细（tenant-scoped，eval_observations）。
type ObservationRepository interface {
	// Save 落库一条观测；obs.ID 由调用方生成，Validate 通过才写入。
	Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
	// Get 按 id 取单条观测。
	Get(ctx context.Context, tenantID, observationID string) (*domain.EvalObservation, error)
	// QueryByResource 按资源 + 可选时间窗分页查询，按创建时间倒序。
	// from/to 为 nil 时不加时间过滤。
	QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
		from, to *time.Time, limit, offset int) ([]domain.EvalObservation, error)
	// FindLatestByTrace 按 trace_id 取最近一条观测（created_at 倒序）；不存在返回 (nil, nil)。
	FindLatestByTrace(ctx context.Context, tenantID, traceID string) (*domain.EvalObservation, error)
	// UpdateBehaviorSignals 合并更新某条观测的 signals.behavior（幂等，不存在不报错）。
	UpdateBehaviorSignals(ctx context.Context, tenantID, observationID string, signals domain.BehaviorSignals) error
}
