package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// BehaviorSignalWriter 把用户行为信号（§4.2）合并到对应 trace 的观测。
// best-effort：找不到观测或更新失败返回错误由调用方决定忽略/告警，不阻断反馈链路。
type BehaviorSignalWriter interface {
	ApplyBehaviorSignals(ctx context.Context, tenantID, traceID string, signals domain.BehaviorSignals) error
}
