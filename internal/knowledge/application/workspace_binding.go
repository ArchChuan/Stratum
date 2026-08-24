package application

import (
	"context"
	"fmt"
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
	}
	return nil
}
