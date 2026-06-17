package port

import "context"

type SkillExecutor interface {
	Execute(ctx context.Context, skillID string, input map[string]any) (map[string]any, error)
}
