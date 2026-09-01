package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// TenantRoleResolver 解析租户成员角色，供删除授权门禁使用。签名与
// agentport.TenantRoleResolver 结构兼容，wiring 可直接传入 c.Agent.RoleResolver。
type TenantRoleResolver interface {
	ResolveTenantRole(context.Context, string, string) (string, error)
}

// 删除实体接口（每实体小接口，消费方定义）：Get*CreatedBy 返回资源创建者
// （未命中 found=false），Delete* 在同一租户事务内完成引用预检 + 硬删除 +
// 变更审计。引用被占用时返回 domain.ErrEntityReferenced；实现必须走
// execTenant 封装（CLAUDE.md 租户边界规则）。

type DeleteSuiteRepository interface {
	GetSuiteCreatedBy(ctx context.Context, tenantID, suiteID string) (string, bool, error)
	DeleteSuite(ctx context.Context, tenantID, suiteID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteRunRepository interface {
	GetRunCreatedBy(ctx context.Context, tenantID, runID string) (string, bool, error)
	DeleteRun(ctx context.Context, tenantID, runID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteJobRepository interface {
	GetJobCreatedBy(ctx context.Context, tenantID, jobID string) (string, bool, error)
	DeleteJob(ctx context.Context, tenantID, jobID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteExperimentRepository interface {
	GetExperimentCreatedBy(ctx context.Context, tenantID, experimentID string) (string, bool, error)
	DeleteExperiment(ctx context.Context, tenantID, experimentID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteCandidateRepository interface {
	GetCandidateCreatedBy(ctx context.Context, tenantID, candidateID string) (string, bool, error)
	DeleteCandidate(ctx context.Context, tenantID, candidateID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteReviewItemRepository interface {
	GetReviewItemCreatedBy(ctx context.Context, tenantID, reviewID string) (string, bool, error)
	DeleteReviewItem(ctx context.Context, tenantID, reviewID string, audit *auditdomain.ResourceChangeAuditEvent) error
}

type DeleteFeedbackRepository interface {
	GetFeedbackCreatedBy(ctx context.Context, tenantID, feedbackID string) (string, bool, error)
	DeleteFeedback(ctx context.Context, tenantID, feedbackID string, audit *auditdomain.ResourceChangeAuditEvent) error
}
