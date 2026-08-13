package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/platformknowledge"
)

// ValidateWorkspaceBindings fails closed when any workspace ID does not
// resolve in the tenant. Consumed by agent/skill application layers through
// the wiring composition root (they must not import knowledge/application).
//
// The bindings are workspace IDs (agent_workspaces.workspace_id is a uuid),
// so resolution is by GetByID — never by name.
//
// Fail-closed semantics:
//   - nil repo → every binding is rejected (dependency unverifiable);
//   - unknown ID or repo error → rejected with the offending ID;
//   - empty ID list → trivially valid (no bindings to verify).
func (s *WorkspaceService) ValidateWorkspaceBindings(ctx context.Context, tenantID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if s.repo == nil {
		return fmt.Errorf("knowledge: workspace binding validation unavailable (repo not wired)")
	}
	for _, id := range ids {
		ws, err := s.repo.GetByID(ctx, tenantID, id)
		if err != nil {
			return fmt.Errorf("knowledge: workspace %q cannot be bound: %w", id, err)
		}
		if ws == nil {
			return fmt.Errorf("knowledge: workspace %q not found", id)
		}
		// 系统内置 workspace(如 stratum_docs)只能由系统助手挂载,普通 agent
		// 不得绑定。GetByID 已 COALESCE 填充 SystemKey/ManagementMode,双条件
		// 与 workspace_service.isPlatformManaged 判定一致。
		if ws.SystemKey == platformknowledge.SystemWorkspaceKey || ws.ManagementMode == platformknowledge.ManagementPlatform {
			return fmt.Errorf("knowledge: workspace %q is platform-managed and cannot be bound: %w", id, domain.ErrPlatformManagedWorkspace)
		}
	}
	return nil
}
