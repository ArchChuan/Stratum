package application

import (
	"context"
	"errors"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

var (
	// ErrFeedbackNotFound 表示删除/查询目标 feedback 不存在。
	// ErrReviewItemNotFound 复用 review_service.go 的既有 sentinel。
	ErrFeedbackNotFound = errors.New("evaluation feedback not found")
)

// DeleteService 提供评测实体的 owner-or-creator 删除。删除门禁 fail-closed：
// roles 未装配、tenant/actor 缺失、角色解析失败一律拒绝（ErrDeleteForbidden）；
// 仅租户 owner 或资源创建者可删。删除为全实体（suites/candidates/experiments/
// runs/jobs/review items/feedback），被引用实体由仓储预检返回 ErrEntityReferenced
// 拒绝删除（禁级联破坏）。
type DeleteService struct {
	roles       port.TenantRoleResolver
	suites      port.DeleteSuiteRepository
	runs        port.DeleteRunRepository
	jobs        port.DeleteJobRepository
	experiments port.DeleteExperimentRepository
	candidates  port.DeleteCandidateRepository
	reviews     port.DeleteReviewItemRepository
	feedback    port.DeleteFeedbackRepository
}

func NewDeleteService(
	roles port.TenantRoleResolver,
	suites port.DeleteSuiteRepository,
	runs port.DeleteRunRepository,
	jobs port.DeleteJobRepository,
	experiments port.DeleteExperimentRepository,
	candidates port.DeleteCandidateRepository,
	reviews port.DeleteReviewItemRepository,
	feedback port.DeleteFeedbackRepository,
) *DeleteService {
	return &DeleteService{
		roles:       roles,
		suites:      suites,
		runs:        runs,
		jobs:        jobs,
		experiments: experiments,
		candidates:  candidates,
		reviews:     reviews,
		feedback:    feedback,
	}
}

// authorize 判定 actor 是否有权删除 createdBy 创建的资源。规则：租户 owner 恒放行；
// 创建者（createdBy 非空且等于 actorID）放行；其余（含 admin 非创建者、member、
// 角色解析失败、空 actor/roles）拒绝。fail-closed，无默认放行。
func (s *DeleteService) authorize(ctx context.Context, tenantID, actorID, createdBy string) error {
	if s.roles == nil || tenantID == "" || actorID == "" {
		return domain.ErrDeleteForbidden
	}
	role, err := s.roles.ResolveTenantRole(ctx, tenantID, actorID)
	if err != nil {
		return domain.ErrDeleteForbidden
	}
	if role == "owner" {
		return nil
	}
	if createdBy != "" && createdBy == actorID {
		return nil
	}
	return domain.ErrDeleteForbidden
}

// newEvaluationDeleteAudit 构造删除变更审计事件（actor=用户、source=api），
// 与变更审计模板一致；实体删除与审计同事务提交（仓储侧实现）。
func newEvaluationDeleteAudit(resourceID, actorID string) *auditdomain.ResourceChangeAuditEvent {
	return &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindEvaluation,
		ResourceID:   resourceID,
		Operation:    auditdomain.ChangeOpDelete,
		ActorID:      actorID,
		ActorType:    auditdomain.ChangeActorUser,
		Source:       auditdomain.ChangeSourceAPI,
	}
}

func (s *DeleteService) DeleteSuite(ctx context.Context, tenantID, suiteID, actorID string) error {
	createdBy, found, err := s.suites.GetSuiteCreatedBy(ctx, tenantID, suiteID)
	if err != nil {
		return err
	}
	if !found {
		return ErrSuiteNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.suites.DeleteSuite(ctx, tenantID, suiteID, newEvaluationDeleteAudit(suiteID, actorID))
}

func (s *DeleteService) DeleteRun(ctx context.Context, tenantID, runID, actorID string) error {
	createdBy, found, err := s.runs.GetRunCreatedBy(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if !found {
		return ErrRunNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.runs.DeleteRun(ctx, tenantID, runID, newEvaluationDeleteAudit(runID, actorID))
}

func (s *DeleteService) DeleteJob(ctx context.Context, tenantID, jobID, actorID string) error {
	createdBy, found, err := s.jobs.GetJobCreatedBy(ctx, tenantID, jobID)
	if err != nil {
		return err
	}
	if !found {
		return ErrJobNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.jobs.DeleteJob(ctx, tenantID, jobID, newEvaluationDeleteAudit(jobID, actorID))
}

func (s *DeleteService) DeleteExperiment(ctx context.Context, tenantID, experimentID, actorID string) error {
	createdBy, found, err := s.experiments.GetExperimentCreatedBy(ctx, tenantID, experimentID)
	if err != nil {
		return err
	}
	if !found {
		return ErrExperimentNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.experiments.DeleteExperiment(ctx, tenantID, experimentID, newEvaluationDeleteAudit(experimentID, actorID))
}

func (s *DeleteService) DeleteCandidate(ctx context.Context, tenantID, candidateID, actorID string) error {
	createdBy, found, err := s.candidates.GetCandidateCreatedBy(ctx, tenantID, candidateID)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrCandidateNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.candidates.DeleteCandidate(ctx, tenantID, candidateID, newEvaluationDeleteAudit(candidateID, actorID))
}

func (s *DeleteService) DeleteReviewItem(ctx context.Context, tenantID, reviewID, actorID string) error {
	createdBy, found, err := s.reviews.GetReviewItemCreatedBy(ctx, tenantID, reviewID)
	if err != nil {
		return err
	}
	if !found {
		return ErrReviewItemNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.reviews.DeleteReviewItem(ctx, tenantID, reviewID, newEvaluationDeleteAudit(reviewID, actorID))
}

func (s *DeleteService) DeleteFeedback(ctx context.Context, tenantID, feedbackID, actorID string) error {
	createdBy, found, err := s.feedback.GetFeedbackCreatedBy(ctx, tenantID, feedbackID)
	if err != nil {
		return err
	}
	if !found {
		return ErrFeedbackNotFound
	}
	if err := s.authorize(ctx, tenantID, actorID, createdBy); err != nil {
		return err
	}
	return s.feedback.DeleteFeedback(ctx, tenantID, feedbackID, newEvaluationDeleteAudit(feedbackID, actorID))
}
