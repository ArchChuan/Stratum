package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// ReviewFilter 评审池列表过滤与分页。
type ReviewFilter struct {
	Status        domain.ReviewItemStatus
	TriggerReason domain.ReviewTriggerReason
	ResourceKind  string
	ResourceID    string
	Limit         int
	Offset        int
}

// ReviewRepository 持久化评审池条目与回写副作用（tenant-scoped；所有方法显式携带
// tenantID，实现必须走 execTenantTx + postgres.WithTenant，见 CLAUDE.md DDL 规则）。
type ReviewRepository interface {
	// UpsertItem 幂等入池：同 (source_type, source_id, trigger_reason) 已存在时
	// 不插入并返回 false（UNIQUE 索引 idx_eval_review_items_dedupe 兜底）。
	UpsertItem(ctx context.Context, tenantID string, item *domain.ReviewItem) (bool, error)
	// GetItem 取单条；不存在返回 (nil, nil)。
	GetItem(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error)
	// ListItems 分页列出；返回条目与总数。
	ListItems(ctx context.Context, tenantID string, f ReviewFilter) ([]domain.ReviewItem, int64, error)
	// MarkReviewed 把条目置为 reviewed 并记录人工结论（状态机唯一写点）。
	MarkReviewed(ctx context.Context, tenantID, id string, verdict domain.HumanVerdict, reviewer, reason string) error
	// CreateCalibrationSample 沉淀 judge 误判校准样本（判 judge_misjudgment 时）。
	CreateCalibrationSample(ctx context.Context, tenantID string, s *domain.CalibrationSample) error
	// CreateAttributionEntry 落产品缺陷归因条目（判 fail / case_revision 时）。
	CreateAttributionEntry(ctx context.Context, tenantID string, e *domain.AttributionEntry) error
	// CountPending 统计待评审条目数（eval_review_backlog Gauge 数据源）。
	CountPending(ctx context.Context, tenantID string) (int64, error)
}

// ReviewEscalator 评审池内联触发入口（观测落库 / 评测集判定后调用，fail-open：
// 实现方不得用返回错误阻断主流程，主流程侧必须忽略升级错误）。
type ReviewEscalator interface {
	// TryEscalateObservation 判定观测是否入池并幂等落条目。返回错误表示升级失败
	// （调用方记日志 + IncEvalReviewEscalateFailure，不得阻断主流程）。
	TryEscalateObservation(ctx context.Context, tenantID string, obs *domain.EvalObservation) error
	// TryEscalateCaseResult 判定评测集 judge 结果是否入池并幂等落条目。
	TryEscalateCaseResult(ctx context.Context, tenantID, runID string,
		result domain.EvalCaseResult, c domain.EvalCase, assertion domain.AssertionResult) error
}
