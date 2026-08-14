package application

import (
	"encoding/json"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
)

// workflowSafeProjection 白名单投影：仅核心元数据，禁止 Spec/InputSchema/节点配置
// （其中可嵌第三方密钥）。
func workflowSafeProjection(d *domain.Definition) map[string]any {
	return map[string]any{"id": d.ID, "name": d.Name, "description": d.Description}
}

// newWorkflowChangeAudit 构造 workflow 资源变更审计事件。actorID 来自 handler
// 认证上下文（auth.sub），禁止从 request body 读取。
func newWorkflowChangeAudit(resourceID, op, actorID string, before, after map[string]any) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindWorkflow,
		ResourceID:   resourceID,
		Operation:    op,
		ActorID:      actorID,
		ActorType:    auditdomain.ChangeActorUser,
		Source:       auditdomain.ChangeSourceAPI,
	}
	var err error
	if before != nil {
		ev.Before, err = json.Marshal(before)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal workflow before: %w", err)
		}
	}
	if after != nil {
		ev.After, err = json.Marshal(after)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal workflow after: %w", err)
		}
	}
	return ev, nil
}
