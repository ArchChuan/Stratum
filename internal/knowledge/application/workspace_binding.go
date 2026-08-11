package application

import (
	"context"
	"fmt"
)

// ValidateWorkspaceBindings fails closed when any workspace name does not
// resolve in the tenant. Consumed by agent/skill application layers through
// the wiring composition root (they must not import knowledge/application).
//
// Fail-closed semantics:
//   - nil repo → every binding is rejected (dependency unverifiable);
//   - unknown name or repo error → rejected with the offending name;
//   - empty name list → trivially valid (no bindings to verify).
func (s *WorkspaceService) ValidateWorkspaceBindings(ctx context.Context, tenantID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if s.repo == nil {
		return fmt.Errorf("knowledge: workspace binding validation unavailable (repo not wired)")
	}
	for _, name := range names {
		ws, err := s.repo.GetByName(ctx, tenantID, name)
		if err != nil {
			return fmt.Errorf("knowledge: workspace %q cannot be bound: %w", name, err)
		}
		if ws == nil {
			return fmt.Errorf("knowledge: workspace %q not found", name)
		}
	}
	return nil
}
