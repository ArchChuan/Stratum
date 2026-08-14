package application

import (
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// experimentSafeProjection 白名单投影：仅被测资源 kind/id 与 status 流转，
// 不携带 revision 载荷、评测指标明细。
func experimentSafeProjection(e domain.Experiment) map[string]any {
	return map[string]any{
		"resource_kind": string(e.ResourceKind),
		"resource_id":   e.ResourceID,
		"status":        string(e.Status),
	}
}

// newExperimentChangeAudit 构造评测实验变更审计事件。审计行 resource_kind
// 恒为 "evaluation"、resource_id 为 experiment.ID（评测操作本身是审计对象）。
func newExperimentChangeAudit(e domain.Experiment, op, actorID string, before, after map[string]any) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindEvaluation,
		ResourceID:   e.ID,
		Operation:    op,
		ActorID:      actorID,
		ActorType:    auditdomain.ChangeActorUser,
		Source:       auditdomain.ChangeSourceAPI,
	}
	var err error
	if before != nil {
		ev.Before, err = json.Marshal(before)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal experiment before: %w", err)
		}
	}
	if after != nil {
		ev.After, err = json.Marshal(after)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal experiment after: %w", err)
		}
	}
	return ev, nil
}
